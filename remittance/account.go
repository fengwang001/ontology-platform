package remittance

import "math/bits"

// account 是单个汇款人的全部可变状态。
type account struct {
	id  string
	lim Limits

	// buckets[day] = 该日仍生效的占用合计（待审核与已出款）。
	// 失败/撤回释放时直接从原占用日扣减；滚动年度窗口之外的桶惰性删除。
	buckets map[int64]int64

	anchorDay  int64 // 上次提交/查询推进到的日序号
	annualUsed int64 // 窗口 (anchorDay-365, anchorDay] 内 buckets 之和

	// pending 是本汇款人全部待审核汇款，按提交顺序排列。
	// 被接受操作的 now 单调不减，故 deadline = submittedAt + R 单调不减，
	// 队首即最早逾期者；逾期物化只需从队首弹出，每笔只弹一次。
	pending []*transfer

	// idem: 幂等键 -> 首次被接受提交的入参与返回（原样重放）。
	idem map[string]idemRecord
}

type idemRecord struct {
	req    SubmitRequest
	result SubmitResult
}

func newAccount(id string, lim Limits) *account {
	return &account{
		id:      id,
		lim:     lim,
		buckets: make(map[int64]int64),
		idem:    make(map[string]idemRecord),
	}
}

// dayIndex 把整数秒换算成自然日序号（向下取整）。
func dayIndex(now int64) int64 { return now / secondsPerDay }

const maxInt64 int64 = 1<<63 - 1

// mulDivFloor 返回 floor(a*b/d)；中间积用 128 位表示避免溢出。
// 结果超过 int64 表示目标额无法表示，由上层按参数非法处理。
func mulDivFloor(a, b, d int64) (int64, bool) {
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	q, _ := bits.Div64(hi, lo, uint64(d))
	if hi != 0 || q > uint64(maxInt64) {
		return 0, false
	}
	return int64(q), true
}

// mulDivCeil 返回 ceil(a*b/d) = (a*b + d - 1) / d，加法与除法均在 128 位域完成。
func mulDivCeil(a, b, d int64) (int64, bool) {
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	addLo, carry := bits.Add64(lo, uint64(d)-1, 0)
	addHi := hi + carry
	q, _ := bits.Div64(addHi, addLo, uint64(d))
	if addHi != 0 || q > uint64(maxInt64) {
		return 0, false
	}
	return int64(q), true
}

// advance 把账户推进到 day：维护 annualUsed 并删除窗口外桶。
//
// 不变量（advance 后）：
//   - buckets 中只保留日序号 > day-365 的键（即窗口 (day-365, day]）；
//   - annualUsed 恰好等于这些桶之和；
//   - anchorDay == day。
//
// 每个被清出窗口的桶只处理一次，日序号跨度大时总循环次数受
// “历史桶总数 + 实际跨过的天数”限制；配合提交前的纯函数投影，
// 单次操作的扫描上界为 365 个桶。
func (a *account) advance(day int64) {
	if len(a.buckets) == 0 {
		a.anchorDay = day
		a.annualUsed = 0
		return
	}
	for a.anchorDay < day {
		a.anchorDay++
		d := a.anchorDay - annualDays
		if v, ok := a.buckets[d]; ok {
			a.annualUsed -= v
			delete(a.buckets, d)
		}
	}
}

// expiredAt 纯统计本账户在 now 时刻“已逾期但尚未物化”的待审核占用，
// 按原占用日聚合。不修改任何状态。
func (a *account) expiredAt(now int64) map[int64]int64 {
	expired := map[int64]int64{}
	for _, t := range a.pending {
		if t.reviewDeadline >= now {
			break // pending 按 deadline 单调不减
		}
		expired[t.day] += t.occupied
	}
	return expired
}

// projectUsage 纯函数式计算“若把账户物化并推进到 now，再追加 occ 占用”后
// 的日占用与滚动年度占用。不修改任何状态，供提交限额校验使用，
// 从而保证被拒绝的提交不留任何痕迹。
//
// 扫描量 <= 365 个窗口桶 + 本次将逾期的队首条目；与该汇款人历史
// 汇款总数及汇款人总数无关。
func (a *account) projectUsage(now, occ int64) (dayUsed, annualUsed int64) {
	day := dayIndex(now)
	expired := a.expiredAt(now)

	annual := a.annualUsed
	// 推进到 day 时滚出年度窗口的整天桶（anchorDay 可能落后于 day）。
	for d := a.anchorDay - annualDays + 1; d <= day-annualDays; d++ {
		annual -= a.buckets[d]
	}
	// 仍在新窗口内、但此刻已逾期的待审核占用会被释放。
	for d, v := range expired {
		if d > day-annualDays {
			annual -= v
		}
	}
	return a.buckets[day] - expired[day] + occ, annual + occ
}

// materialize 把 deadline < now 的待审核汇款物化为逾期失败并释放占用。
// 只能在“确定会被接受”的操作路径上调用（提交已通过全部校验、
// 或审核/撤回/查询本身）。失败生效时刻记为 deadline+1。
func (a *account) materialize(now int64) []*transfer {
	i := 0
	for ; i < len(a.pending); i++ {
		t := a.pending[i]
		if t.reviewDeadline >= now {
			break
		}
		t.status = StatusFailed
		t.decidedAt = t.reviewDeadline + 1
	}
	failed := a.pending[:i]
	a.pending = append(a.pending[:0:0], a.pending[i:]...)
	return failed
}

func (a *account) addPending(t *transfer) { a.pending = append(a.pending, t) }

// removePending 从待审核队列中移除指定汇款（批准/拒绝/撤回）。
// 队列通常极短且按提交序排列；其长度是“当前仍待审核”的数量，
// 与历史汇款总数无关。
func (a *account) removePending(t *transfer) bool {
	for i, p := range a.pending {
		if p == t {
			a.pending = append(a.pending[:i], a.pending[i+1:]...)
			return true
		}
	}
	return false
}

// reserve 提交一笔占用到 day。
func (a *account) reserve(day int64, occ int64) {
	a.buckets[day] += occ
	a.annualUsed += occ
}

// release 从原占用日 releaseDay 释放 occ。
// releaseDay 可能已滚出窗口（跨年度后才失败）：该桶已不计入 annualUsed，
// 删除键即可，绝不允许记到失败发生的当日。
func (a *account) release(releaseDay int64, occ int64) {
	v := a.buckets[releaseDay] - occ
	if v <= 0 {
		delete(a.buckets, releaseDay)
	} else {
		a.buckets[releaseDay] = v
	}
	if releaseDay > a.anchorDay-annualDays {
		a.annualUsed -= occ
	}
}
