package settlement

import (
	"container/heap"
	"math"
)

// dayHeap 营业日最小堆，配合 pendingSum 使用，每个发生日只入堆一次。
type dayHeap []int64

func (h dayHeap) Len() int           { return len(h) }
func (h dayHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h dayHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *dayHeap) Push(x any)        { *h = append(*h, x.(int64)) }
func (h *dayHeap) Pop() any {
	old := *h
	n := len(old)
	v := old[n-1]
	*h = old[:n-1]
	return v
}

// reserveBatch 保证金批次（内部表示，与 ReserveBatch 字段一致）。
type reserveBatch struct {
	retentionDay int64
	releaseDay   int64 // 无对应营业日时为 math.MaxInt64（永不到期）
	balance      int64 // 未动用余额，恒大于 0
}

// merchant 商户的内部结算状态。
//
// 关键不变式：cumulativePayout + reserveBalance + carry == settledTxSum。
// 批次队列 batches[batchHead:] 按留存日升序（等于到期日升序），
// 每个批次只经历一次入队、至多一次到期弹出与若干次队首动用，
// 因此单日结算开销与历史流水总数、历史批次总数无关。
type merchant struct {
	cfg Config

	lastSettled      int64 // 已结算到的最近营业日，未结算过为 math.MinInt64
	carry            int64 // 结转负余额（<= 0）
	cumulativePayout int64
	reserveBalance   int64
	settledTxSum     int64

	txIDs        map[string]struct{}
	pendingAgg   map[int64]pendingDay // 未结算流水按发生日聚合
	pendingDays  dayHeap              // pendingAgg 的键构成的最小堆
	pendingCount int                  // 未结算流水笔数

	batches   []reserveBatch // 队列，仅追加
	batchHead int            // 队首下标，之前的批次已释放或被耗尽
}

// pendingDay 某一发生日的未结算流水聚合。
type pendingDay struct {
	sum   int64
	count int
}

func newMerchant(cfg Config) *merchant {
	return &merchant{
		cfg:         cfg,
		lastSettled: math.MinInt64,
		txIDs:       make(map[string]struct{}),
		pendingAgg:  make(map[int64]pendingDay),
	}
}

// ceilReserve 计算 ceil(net * bps / 10000)，net >= 0。
// 拆分 net = q*10000 + r 计算，避免 net*bps 溢出 int64。
func ceilReserve(net int64, bps int) int64 {
	q, r := net/10000, net%10000
	res := q * int64(bps)
	rem := r * int64(bps) // < 10000*10000，不会溢出
	res += rem / 10000
	if rem%10000 != 0 {
		res++
	}
	return res
}

// postTx 在引擎完成前置校验（参数、时钟、商户存在）后录入流水。
// 依次检查：流水编号重复 > 日期非法 > 已封账；任何失败都不改变状态。
func (m *merchant) postTx(now int64, txID string, day int64, amount int64) error {
	if _, dup := m.txIDs[txID]; dup {
		return newError(CodeDuplicateTxID, "duplicate tx id %q", txID)
	}
	if day > now {
		return newError(CodeInvalidDate, "tx day %d is after now %d", day, now)
	}
	if day < m.lastSettled {
		return newError(CodeAlreadyClosed, "tx day %d is before last settled day %d", day, m.lastSettled)
	}
	m.txIDs[txID] = struct{}{}
	agg, ok := m.pendingAgg[day]
	if !ok {
		heap.Push(&m.pendingDays, day)
	}
	agg.sum += amount
	agg.count++
	m.pendingAgg[day] = agg
	m.pendingCount++
	return nil
}

// settleDay 结算单个营业日 t，返回出款记录。调用方保证 t 是营业日且
// 严格晚于 m.lastSettled，逐日调用与追赶调用结果完全一致。
func (m *merchant) settleDay(cal Calendar, t int64) PayoutRecord {
	// 1. 新到期流水：发生日不晚于 t 往前数第 N 个营业日（含）。
	var newlySettled int64
	if cutoff, ok := cal.Retreat(t, m.cfg.DelayDays); ok {
		for len(m.pendingDays) > 0 && m.pendingDays[0] <= cutoff {
			d := heap.Pop(&m.pendingDays).(int64)
			agg := m.pendingAgg[d]
			newlySettled += agg.sum
			m.pendingCount -= agg.count
			delete(m.pendingAgg, d)
		}
	}
	m.settledTxSum += newlySettled
	net := m.carry + newlySettled

	// 2. 到期释放：留存日之后第 H 个营业日（含该日）到达的批次，释放未动用余额。
	var release int64
	for m.batchHead < len(m.batches) && m.batches[m.batchHead].releaseDay <= t {
		release += m.batches[m.batchHead].balance
		m.batchHead++
	}

	var newReserve, drawn, payout int64
	if net >= 0 {
		// 非负：留存新批次，出款 = 净额 - 新留存 + 释放。
		newReserve = ceilReserve(net, m.cfg.ReserveBps)
		if newReserve > 0 {
			releaseDay, ok := cal.Advance(t, m.cfg.HorizonDays)
			if !ok {
				releaseDay = math.MaxInt64
			}
			m.batches = append(m.batches, reserveBatch{retentionDay: t, releaseDay: releaseDay, balance: newReserve})
		}
		payout = net - newReserve + release
		m.carry = 0
	} else {
		// 负：到期释放先入账；仍为负则按留存日由早到晚动用未到期批次补足至零。
		balance := net + release
		if balance < 0 {
			need := -balance
			for need > 0 && m.batchHead < len(m.batches) {
				b := &m.batches[m.batchHead]
				if b.balance <= need {
					drawn += b.balance
					need -= b.balance
					m.batchHead++
				} else {
					b.balance -= need
					drawn += need
					need = 0
				}
			}
			balance += drawn
		}
		if balance < 0 {
			m.carry = balance // 不足部分结转到下一营业日
			payout = 0
		} else {
			m.carry = 0
			payout = balance
		}
	}

	m.reserveBalance += newReserve - release - drawn
	m.cumulativePayout += payout
	m.lastSettled = t
	m.compactBatches()

	return PayoutRecord{
		Day:        t,
		Net:        net,
		Release:    release,
		NewReserve: newReserve,
		Drawn:      drawn,
		Payout:     payout,
		CarryAfter: m.carry,
	}
}

// compactBatches 在队首空洞占比过高时压缩批次队列，保持内存与历史批次总数无关。
func (m *merchant) compactBatches() {
	if m.batchHead >= 64 && m.batchHead*2 >= len(m.batches) {
		m.batches = append([]reserveBatch(nil), m.batches[m.batchHead:]...)
		m.batchHead = 0
	}
}

func (m *merchant) snapshot(id string) Snapshot {
	batches := make([]ReserveBatch, 0, len(m.batches)-m.batchHead)
	for _, b := range m.batches[m.batchHead:] {
		batches = append(batches, ReserveBatch{
			RetentionDay: b.retentionDay,
			ReleaseDay:   b.releaseDay,
			Balance:      b.balance,
		})
	}
	return Snapshot{
		MerchantID:       id,
		LastSettledDay:   m.lastSettled,
		CumulativePayout: m.cumulativePayout,
		ReserveBalance:   m.reserveBalance,
		Carry:            m.carry,
		SettledTxSum:     m.settledTxSum,
		PendingTxCount:   m.pendingCount,
		Batches:          batches,
	}
}
