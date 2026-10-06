package billing

import "sync"

// SlotSeconds 是每个槽位的固定时长（秒）。
const SlotSeconds = 300

// 取值边界。
const (
	MaxRate    = 1_000_000_000_000 // 10^12
	MaxSlots   = 1_000_000
	MaxBasisPt = 10_000 // 缺失容忍比例的分母（万分之一）
)

// Sample 是一个槽位的入向/出向速率采样及其版本号。
type Sample struct {
	Slot    int
	Ingress int64
	Egress  int64
	Version int64
}

// slotState 记录某槽位当前被接受的采样与该槽位曾见过的最高版本号。
type slotState struct {
	ingress     int64
	egress      int64
	version     int64 // 当前采样版本；0 表示当前缺失
	highestSeen int64 // 曾接受/幂等见过的最高版本，撤回后保留
}

// Overview 是周期概览。
type Overview struct {
	ReceivedSlots int   // 当前持有采样的槽位数（即有效槽位数 K）
	MissingSlots  int   // 当前缺失槽位数 N-K
	BilledRate    int64 // 当前计费速率；无采样时为 0 且 RateDefined=false
	RateDefined   bool  // 计费速率是否有定义
	Settled       bool  // 周期是否已结算
}

// SettlementResult 是结算结果。
type SettlementResult struct {
	BilledRate    int64 // 计费速率
	Base          int64 // 金额基数：max(承诺速率, 计费速率)
	ValidSlots    int   // 有效槽位数 K
	MissingSlots  int   // 缺失槽位数 N-K
	Repeated      bool  // 是否为结算封存后的重复返回
	CommittedRate int64 // 首次结算使用的承诺速率
	ToleranceBp   int   // 首次结算使用的缺失容忍比例（万分之一）
}

// Settler 是单个计费周期的结算器。
//
// 所有方法均在互斥锁保护下执行，因此并发调用的结果严格等价于
// 某个串行顺序（内部线性化点为锁获取点）。提交采样与查询计费速率
// 对槽位状态是 O(1)、对有效值多重集是期望 O(log K)，均不随已收到
// 采样数线性增长（见 billing/treap.go 与复杂度验证测试）。
type Settler struct {
	mu        sync.RWMutex
	startUnix int64
	n         int
	slots     []slotState
	values    *treap // 当前全部有效槽位有效值构成的多重集
	settled   bool
	result    SettlementResult
}

// NewSettler 创建一个计费周期结算器。
// startUnix 为周期起始 Unix 时间（秒），n 为槽位个数（1..10^6）。
func NewSettler(startUnix int64, n int) *Settler {
	if n < 1 || n > MaxSlots {
		panic("billing: n 必须在 1..10^6 之间")
	}
	return &Settler{
		startUnix: startUnix,
		n:         n,
		slots:     make([]slotState, n),
		values:    newTreap(),
	}
}

// StartUnix 返回周期起始 Unix 时间（秒）。
func (s *Settler) StartUnix() int64 { return s.startUnix }

// N 返回槽位个数。
func (s *Settler) N() int { return s.n }

// SlotInterval 返回下标 slot 的半开时间区间 [start, end)（Unix 秒）。
func (s *Settler) SlotInterval(slot int) (start int64, end int64, err error) {
	if slot < 0 || slot >= s.n {
		return 0, 0, errInvalid("槽位下标越界: %d（周期含 %d 个槽位）", slot, s.n)
	}
	start = s.startUnix + int64(slot)*SlotSeconds
	return start, start + SlotSeconds, nil
}

func effectiveValue(ingress, egress int64) int64 {
	if ingress > egress {
		return ingress
	}
	return egress
}

// discardedCount 返回有效槽位数为 k 时应丢弃的最大槽位数量：floor(k*5/100)。
func discardedCount(k int) int { return k * 5 / 100 }

func (s *Settler) validateSample(sample Sample) error {
	if sample.Slot < 0 || sample.Slot >= s.n {
		return errInvalid("槽位下标越界: %d（周期含 %d 个槽位）", sample.Slot, s.n)
	}
	if sample.Ingress < 0 || sample.Ingress > MaxRate {
		return errInvalid("入向速率越界: %d（允许 0..10^12）", sample.Ingress)
	}
	if sample.Egress < 0 || sample.Egress > MaxRate {
		return errInvalid("出向速率越界: %d（允许 0..10^12）", sample.Egress)
	}
	if sample.Version <= 0 {
		return errInvalid("版本号必须为正整数: %d", sample.Version)
	}
	return nil
}

// Submit 提交一个槽位采样。
//
// 接受规则（同槽位）：
//   - 版本号 > 该槽位曾见最高版本：覆盖（当前缺失则新接受）；
//   - 版本号 == 当前采样版本且内容完全相同：幂等重复，状态不变；
//   - 版本号 == 当前采样版本但内容不同：版本冲突；
//   - 版本号 <= 曾见最高版本的其他情形：过期采样。
func (s *Settler) Submit(sample Sample) error {
	if err := s.validateSample(sample); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 错误固定优先级：参数非法 > 已结算 > 版本冲突/过期采样。
	if s.settled {
		return errSettled("周期已结算，拒绝提交槽位 %d", sample.Slot)
	}

	cur := &s.slots[sample.Slot]
	value := effectiveValue(sample.Ingress, sample.Egress)

	if cur.version != 0 {
		// 槽位当前持有采样。
		switch {
		case sample.Version > cur.highestSeen:
			// 更高版本覆盖；先移除旧有效值再加入新值，保证被拒时状态不变。
			s.values.remove(effectiveValue(cur.ingress, cur.egress))
			s.values.insert(value)
			cur.ingress = sample.Ingress
			cur.egress = sample.Egress
			cur.version = sample.Version
			cur.highestSeen = sample.Version
			return nil
		case sample.Version == cur.version:
			if cur.ingress == sample.Ingress && cur.egress == sample.Egress {
				return nil // 幂等重复，不改变任何状态
			}
			return errConflict("槽位 %d 版本 %d 已存在且内容不同", sample.Slot, sample.Version)
		default:
			// sample.Version < cur.version <= cur.highestSeen
			return errStale("槽位 %d 版本 %d 已过期（当前版本 %d）", sample.Slot, sample.Version, cur.version)
		}
	}

	// 槽位当前缺失（可能从未收到，也可能被撤回）。
	if sample.Version <= cur.highestSeen {
		return errStale("槽位 %d 版本 %d 不高于曾见最高版本 %d", sample.Slot, sample.Version, cur.highestSeen)
	}
	s.values.insert(value)
	cur.ingress = sample.Ingress
	cur.egress = sample.Egress
	cur.version = sample.Version
	cur.highestSeen = sample.Version
	return nil
}

// Withdraw 撤回指定槽位当前版本恰为 version 的采样。
// 撤回后槽位回到缺失，但保留版本门槛（highestSeen）。
func (s *Settler) Withdraw(slot int, version int64) error {
	if slot < 0 || slot >= s.n {
		return errInvalid("槽位下标越界: %d（周期含 %d 个槽位）", slot, s.n)
	}
	if version <= 0 {
		return errInvalid("版本号必须为正整数: %d", version)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 错误固定优先级：参数非法 > 已结算 > 版本不符 > 不存在。
	if s.settled {
		return errSettled("周期已结算，拒绝撤回槽位 %d", slot)
	}

	cur := &s.slots[slot]
	if cur.version == 0 {
		return errNotFound("槽位 %d 当前无采样", slot)
	}
	if cur.version != version {
		return errMismatch("槽位 %d 当前版本为 %d，与撤回版本 %d 不符", slot, cur.version, version)
	}

	s.values.remove(effectiveValue(cur.ingress, cur.egress))
	cur.ingress = 0
	cur.egress = 0
	cur.version = 0
	// highestSeen 故意保留：撤回不清除曾见最高版本号。
	return nil
}

// CurrentRate 返回当前计费速率。
// K=0 时返回 CodeNoSamples 错误。
func (s *Settler) CurrentRate() (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentRateLocked()
}

// 调用方持有读锁或写锁。
func (s *Settler) currentRateLocked() (int64, error) {
	k := s.values.total()
	if k == 0 {
		return 0, errNoSamples("当前无任何有效采样，计费速率无定义")
	}
	drop := discardedCount(k)
	// 丢弃 drop 个最大的，剩下的最大值即第 drop+1 大；并列值各自占位。
	return s.values.kthLargest(drop+1, nil), nil
}

// Settle 结算周期。
//
// committedRate 为承诺速率（0..10^12）；toleranceBp 为缺失容忍比例，
// 以万分之一为单位（0..10000）。当 missing*10000 > toleranceBp*N
// 时报数据不足且不改变任何状态。结算成功后周期封存；此后重复调用
// 一律返回首次结果（Repeated=true），参数被忽略。
func (s *Settler) Settle(committedRate int64, toleranceBp int) (SettlementResult, error) {
	if committedRate < 0 || committedRate > MaxRate {
		return SettlementResult{}, errInvalid("承诺速率越界: %d（允许 0..10^12）", committedRate)
	}
	if toleranceBp < 0 || toleranceBp > MaxBasisPt {
		return SettlementResult{}, errInvalid("缺失容忍比例越界: %d（允许 0..10000，单位万分之一）", toleranceBp)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.settled {
		r := s.result
		r.Repeated = true
		return r, nil
	}

	k := s.values.total()
	missing := s.n - k

	// 无采样（K=0）优先级高于数据不足：两者都是“无法给出计费速率”，
	// 按规定错误优先级取“无采样”。
	if k == 0 {
		return SettlementResult{}, errNoSamples("当前无任何有效采样，无法结算")
	}

	// 严格大于才拒绝：missing*10000 == toleranceBp*N 恰在容忍边界内。
	if int64(missing)*MaxBasisPt > int64(toleranceBp)*int64(s.n) {
		return SettlementResult{}, errInsufficient(
			"数据不足：缺失 %d/%d 槽位，超过容忍比例 %d/10000",
			missing, s.n, toleranceBp)
	}

	rate := s.values.kthLargest(discardedCount(k)+1, nil)
	base := committedRate
	if rate > base {
		base = rate
	}

	r := SettlementResult{
		BilledRate:    rate,
		Base:          base,
		ValidSlots:    k,
		MissingSlots:  missing,
		Repeated:      false,
		CommittedRate: committedRate,
		ToleranceBp:   toleranceBp,
	}
	s.settled = true
	s.result = r
	return r, nil
}

// Overview 返回周期概览，不改变任何状态。
func (s *Settler) Overview() Overview {
	s.mu.RLock()
	defer s.mu.RUnlock()

	k := s.values.total()
	o := Overview{
		ReceivedSlots: k,
		MissingSlots:  s.n - k,
		Settled:       s.settled,
	}
	if rate, err := s.currentRateLocked(); err == nil {
		o.BilledRate = rate
		o.RateDefined = true
	}
	return o
}

// IsSettled 返回周期是否已结算封存。
func (s *Settler) IsSettled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settled
}

// HighestSeenVersion 返回指定槽位曾见的最高版本号（撤回后仍保留），
// 主要用于测试与可观测。槽位下标越界时返回错误。
func (s *Settler) HighestSeenVersion(slot int) (int64, error) {
	if slot < 0 || slot >= s.n {
		return 0, errInvalid("槽位下标越界: %d（周期含 %d 个槽位）", slot, s.n)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.slots[slot].highestSeen, nil
}
