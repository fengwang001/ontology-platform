package billing

import "sync"

// 固定槽位长度（秒）。
const SlotSeconds = 300

// 值与承诺速率的合法范围。
const (
	MaxRate      = int64(1_000_000_000_000)
	MaxSlotCount = 1_000_000
	MaxTolerance = 10_000
)

// Sample 是一次入向/出向带宽采样。
type Sample struct {
	Slot     int
	Inbound  int64
	Outbound int64
	Version  int64
}

// Overview 是周期概览快照。RateDefined 为 false 时 Rate 无定义。
type Overview struct {
	ReceivedSlots int
	MissingSlots  int
	RateDefined   bool
	Rate          int64
	Settled       bool
}

// Settlement 是结算结果。Repeated 为 true 表示返回的是首次结算的缓存结果。
type Settlement struct {
	BilledRate   int64
	Base         int64
	ValidSlots   int
	MissingSlots int
	Repeated     bool
}

type slotState struct {
	has      bool
	version  int64
	inbound  int64
	outbound int64
	// highestVersion 永不因撤回而回退。
	highestVersion int64
}

// Biller 是单个计费周期的结算器；方法可并发调用。
type Biller struct {
	mu sync.Mutex

	start int64
	n     int

	slots []slotState
	// values 是当前所有有效槽位有效值的多重集，大小恒等于有效槽位数。
	values *multiset

	settled    bool
	settlement *Settlement
}

// NewBiller 创建结算器。start 为周期起始时刻（调用方自定义的整数时间戳，
// 仅作记录，槽位由下标 0..n-1 确定）；n 必须在 [1, 10^6]。
func NewBiller(start int64, n int) (*Biller, error) {
	if n < 1 || n > MaxSlotCount {
		return nil, bizError(ErrInvalidArgument, "slot count out of range [1, 1000000]")
	}
	return &Biller{
		start:  start,
		n:      n,
		slots:  make([]slotState, n),
		values: &multiset{},
	}, nil
}

func validSample(s Sample, n int) bool {
	return s.Slot >= 0 && s.Slot < n &&
		s.Inbound >= 0 && s.Inbound <= MaxRate &&
		s.Outbound >= 0 && s.Outbound <= MaxRate &&
		s.Version > 0
}

// Submit 提交或更新一个采样。
//
// 判定优先级（高到低）：参数非法 -> 已结算 -> 版本冲突 / 过期采样 -> 接受。
func (b *Biller) Submit(s Sample) error {
	if !validSample(s, b.n) {
		return bizError(ErrInvalidArgument, "slot/value/version out of range")
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.settled {
		return bizError(ErrAlreadySettled, "billing period is settled")
	}

	cur := &b.slots[s.Slot]
	if s.Version <= cur.highestVersion {
		if cur.has && s.Version == cur.version &&
			s.Inbound == cur.inbound && s.Outbound == cur.outbound {
			// 幂等重复：内容完全相同，状态不变。
			return nil
		}
		if cur.has && s.Version == cur.version {
			// 同版本不同内容：先于过期判定报版本冲突。
			return bizError(ErrVersionConflict, "same version with different payload")
		}
		return bizError(ErrStaleSample, "version not greater than highest seen")
	}

	if cur.has {
		b.values.remove(effectiveValue(cur.inbound, cur.outbound))
	}
	b.values.add(effectiveValue(s.Inbound, s.Outbound))
	cur.has = true
	cur.version = s.Version
	cur.inbound = s.Inbound
	cur.outbound = s.Outbound
	cur.highestVersion = s.Version
	return nil
}

// Withdraw 撤回指定槽位上恰为 version 的当前采样。
//
// 判定优先级（高到低）：参数非法 -> 已结算 -> 版本不符 -> 不存在。
// 撤回不清空该槽位曾见过的最高版本号。
func (b *Biller) Withdraw(slot int, version int64) error {
	if slot < 0 || slot >= b.n || version <= 0 {
		return bizError(ErrInvalidArgument, "slot/version out of range")
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.settled {
		return bizError(ErrAlreadySettled, "billing period is settled")
	}

	cur := &b.slots[slot]
	if !cur.has {
		return bizError(ErrNotFound, "slot has no current sample")
	}
	if cur.version != version {
		return bizError(ErrVersionMismatch, "current version differs from requested")
	}

	b.values.remove(effectiveValue(cur.inbound, cur.outbound))
	cur.has = false
	// version 与负载保留与否不影响外部行为；清空以严格表示“无当前采样”。
	cur.version = 0
	cur.inbound = 0
	cur.outbound = 0
	return nil
}

// billedRateLocked 调用方必须已持锁。
func (b *Biller) billedRateLocked() (int64, bool) {
	k := b.values.size()
	if k == 0 {
		return 0, false
	}
	discard := k * 5 / 100
	// 丢弃 discard 个最大有效值后，剩余最大值即第 discard+1 大。
	return b.values.kthLargest(discard + 1), true
}

// CurrentRate 返回截至当前的计费速率；无任何有效槽位时返回 ErrNoSamples。
func (b *Biller) CurrentRate() (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	rate, ok := b.billedRateLocked()
	if !ok {
		return 0, bizError(ErrNoSamples, "no valid slots")
	}
	return rate, nil
}

// Settle 结算并封存周期。
//
// 判定顺序：参数非法 -> 已结算（首次）/ 重复返回 ->
// 数据不足 -> 无采样（计费速率无定义）-> 成功封存。
// 数据不足与无采样均不改变任何状态，可补齐采样后重新结算。
func (b *Biller) Settle(committedRate int64, tolerancePerTenThousand int) (*Settlement, error) {
	if committedRate < 0 || committedRate > MaxRate ||
		tolerancePerTenThousand < 0 || tolerancePerTenThousand > MaxTolerance {
		return nil, bizError(ErrInvalidArgument, "committed rate/tolerance out of range")
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.settled {
		cp := *b.settlement
		cp.Repeated = true
		return &cp, nil
	}

	valid := b.values.size()
	missing := b.n - valid
	// missing * 10000 > tolerance * N
	if int64(missing)*10000 > int64(tolerancePerTenThousand)*int64(b.n) {
		return nil, bizError(ErrInsufficientData, "missing ratio exceeds tolerance")
	}

	rate, ok := b.billedRateLocked()
	if !ok {
		return nil, bizError(ErrNoSamples, "no valid slots")
	}

	base := committedRate
	if rate > base {
		base = rate
	}
	result := &Settlement{
		BilledRate:   rate,
		Base:         base,
		ValidSlots:   valid,
		MissingSlots: missing,
		Repeated:     false,
	}
	b.settled = true
	b.settlement = result
	cp := *result
	return &cp, nil
}

// Overview 读取周期概览，不改变任何状态。
func (b *Biller) Overview() Overview {
	b.mu.Lock()
	defer b.mu.Unlock()

	received := b.values.size()
	o := Overview{
		ReceivedSlots: received,
		MissingSlots:  b.n - received,
		Settled:       b.settled,
	}
	if rate, ok := b.billedRateLocked(); ok {
		o.RateDefined = true
		o.Rate = rate
	}
	return o
}

func effectiveValue(inbound, outbound int64) int64 {
	if inbound > outbound {
		return inbound
	}
	return outbound
}
