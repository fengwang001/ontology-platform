package billing

import "sort"

// NaiveBiller 是与 Biller 行为一致的独立朴素参考实现：
// 直接保存槽位状态，查询时对全部有效值排序。
// 仅供测试对照，不保证提交/查询为次线性开销。
type NaiveBiller struct {
	mu    noCopyMutex
	start int64
	n     int
	slots []slotState

	settled    bool
	settlement *Settlement
}

// noCopyMutex 与 Biller 的 sync.Mutex 区分开：朴素模型用独立实现，
// 避免共享同一套同步代码造成对照失真（此处不做真实并发，仅占位）。
type noCopyMutex struct{}

func (noCopyMutex) Lock()   {}
func (noCopyMutex) Unlock() {}

// NewNaiveBiller 创建朴素参考结算器。
func NewNaiveBiller(start int64, n int) *NaiveBiller {
	if n < 1 || n > MaxSlotCount {
		return nil
	}
	return &NaiveBiller{start: start, n: n, slots: make([]slotState, n)}
}

func (b *NaiveBiller) Submit(s Sample) error {
	if !validSample(s, b.n) {
		return bizError(ErrInvalidArgument, "slot/value/version out of range")
	}
	if b.settled {
		return bizError(ErrAlreadySettled, "billing period is settled")
	}

	cur := &b.slots[s.Slot]
	if s.Version <= cur.highestVersion {
		if cur.has && s.Version == cur.version &&
			s.Inbound == cur.inbound && s.Outbound == cur.outbound {
			return nil
		}
		if cur.has && s.Version == cur.version {
			return bizError(ErrVersionConflict, "same version with different payload")
		}
		return bizError(ErrStaleSample, "version not greater than highest seen")
	}

	cur.has = true
	cur.version = s.Version
	cur.inbound = s.Inbound
	cur.outbound = s.Outbound
	cur.highestVersion = s.Version
	return nil
}

func (b *NaiveBiller) Withdraw(slot int, version int64) error {
	if slot < 0 || slot >= b.n || version <= 0 {
		return bizError(ErrInvalidArgument, "slot/version out of range")
	}
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

	cur.has = false
	cur.version = 0
	cur.inbound = 0
	cur.outbound = 0
	return nil
}

// sortedValues 收集所有有效值并从大到小排序——这是与 treap
// 完全独立的“按定义直写”实现。
func (b *NaiveBiller) sortedValues() []int64 {
	values := make([]int64, 0, b.n)
	for i := range b.slots {
		s := &b.slots[i]
		if s.has {
			values = append(values, effectiveValue(s.inbound, s.outbound))
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i] > values[j] })
	return values
}

func (b *NaiveBiller) CurrentRate() (int64, error) {
	values := b.sortedValues()
	if len(values) == 0 {
		return 0, bizError(ErrNoSamples, "no valid slots")
	}
	discard := len(values) * 5 / 100
	return values[discard], nil
}

func (b *NaiveBiller) Settle(committedRate int64, tolerancePerTenThousand int) (*Settlement, error) {
	if committedRate < 0 || committedRate > MaxRate ||
		tolerancePerTenThousand < 0 || tolerancePerTenThousand > MaxTolerance {
		return nil, bizError(ErrInvalidArgument, "committed rate/tolerance out of range")
	}
	if b.settled {
		cp := *b.settlement
		cp.Repeated = true
		return &cp, nil
	}

	values := b.sortedValues()
	valid := len(values)
	missing := b.n - valid
	if int64(missing)*10000 > int64(tolerancePerTenThousand)*int64(b.n) {
		return nil, bizError(ErrInsufficientData, "missing ratio exceeds tolerance")
	}
	if valid == 0 {
		return nil, bizError(ErrNoSamples, "no valid slots")
	}

	discard := valid * 5 / 100
	rate := values[discard]
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

func (b *NaiveBiller) Overview() Overview {
	values := b.sortedValues()
	o := Overview{
		ReceivedSlots: len(values),
		MissingSlots:  b.n - len(values),
		Settled:       b.settled,
	}
	if len(values) > 0 {
		discard := len(values) * 5 / 100
		o.RateDefined = true
		o.Rate = values[discard]
	}
	return o
}
