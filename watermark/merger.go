package watermark

import "sync"

// Time 是调用方注入的处理时间标量。时间的物理含义（毫秒、微秒、事件序号）
// 由调用方约定，组件只要求其为非负、可比较、可做差的单调序列。
type Time int64

// Watermark 是分区上报的水位标量，语义与取值域由调用方约定，合法域为非负整数。
type Watermark int64

// Config 是构造合并水位组件的参数。
type Config struct {
	// Partitions 是固定分区数量，必须大于 0。
	Partitions int

	// IdleThreshold 是分区空闲阈值：处理时间 - 最后活跃时间 >= 该值时分区空闲。
	// 必须 >= 0；为 0 时，分区在上报后的下一次计算（elapsed=0 即达阈值）立即空闲。
	IdleThreshold Time

	// Logger 可选，用于记录每次操作的输入、判定依据与合并水位推进情况；
	// 为 nil 时不输出日志。
	Logger Logger
}

// partitionState 是单个分区的内部状态。
type partitionState struct {
	watermark    Watermark // 该分区最近一次上报的水位
	lastActive   Time      // 该分区最后一次活跃（上报）时的处理时间
	hasWatermark bool      // 是否曾上报过水位
}

// Merger 合并多个分区上报的水位，输出单调前进的合并水位。
// 所有方法对并发调用安全：写操作互斥，读操作可与写操作并发且读到一致快照。
type Merger struct {
	mu sync.RWMutex

	partitions    []partitionState
	idleThreshold Time

	// now 是最近一次注入的处理时间；clockSet 标记时钟是否已初始化。
	now      Time
	clockSet bool

	// merged 是当前对外可见的合并水位，只进不退。
	merged Watermark

	logger Logger
}

// New 按给定配置构造合并水位组件。
// 配置非法（分区数 <= 0、空闲阈值 < 0）时返回包装了 ErrInvalidConfig 的错误。
func New(cfg Config) (*Merger, error) {
	if cfg.Partitions <= 0 {
		return nil, invalidConfigf("Partitions must be > 0, got %d", cfg.Partitions)
	}
	if cfg.IdleThreshold < 0 {
		return nil, invalidConfigf("IdleThreshold must be >= 0, got %d", cfg.IdleThreshold)
	}
	return &Merger{
		partitions:    make([]partitionState, cfg.Partitions),
		idleThreshold: cfg.IdleThreshold,
		logger:        cfg.Logger,
	}, nil
}

// AdvanceClock 注入新的处理时间并重新计算合并水位。
// now 为负返回 ErrInvalidArgument；早于已注入时间返回 ErrClockRollback，
// 两种情况下状态均保持不变。返回重算后的合并水位。
func (m *Merger) AdvanceClock(now Time) (Watermark, error) {
	if now < 0 {
		return 0, invalidArgumentf("processing time must be >= 0, got %d", now)
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.clockSet && now < m.now {
		return m.merged, clockRollbackf("processing time %d is before last injected time %d", now, m.now)
	}

	prevClock := m.now
	prevClockSet := m.clockSet
	m.now = now
	m.clockSet = true

	merged := m.recomputeLocked(decision{
		op: "advance_clock", at: now,
		prevClock: prevClock, prevClockSet: prevClockSet,
	})
	return merged, nil
}

// Report 上报某分区在处理时间 at 时的水位 w 并重新计算合并水位。
// at 不得早于已注入的处理时间（允许相等）；w 不得低于该分区此前水位。
// 任一检查失败时返回对应错误且分区水位、活跃时间、时钟与合并水位均保持不变。
// 返回重算后的合并水位。
func (m *Merger) Report(partition int, at Time, w Watermark) (Watermark, error) {
	// 校验顺序固定：参数合法性 -> 分区越界 -> 时钟回退 -> 分区水位回退。
	if at < 0 {
		return 0, invalidArgumentf("processing time must be >= 0, got %d", at)
	}
	if w < 0 {
		return 0, invalidArgumentf("partition watermark must be >= 0, got %d", w)
	}
	if partition < 0 || partition >= len(m.partitions) {
		return 0, partitionOutOfRangef("partition %d out of range [0,%d)", partition, len(m.partitions))
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.clockSet && at < m.now {
		return m.merged, clockRollbackf("report time %d is before last injected time %d", at, m.now)
	}
	p := &m.partitions[partition]
	if p.hasWatermark && w < p.watermark {
		return m.merged, watermarkRollbackf(
			"partition %d watermark %d is below its previous watermark %d", partition, w, p.watermark)
	}

	prevClock := m.now
	prevClockSet := m.clockSet
	prevPartitionWM := p.watermark

	m.now = at
	m.clockSet = true
	p.watermark = w
	p.lastActive = at
	p.hasWatermark = true

	merged := m.recomputeLocked(decision{
		op: "report", partition: partition, at: at, reported: w, hasReport: true,
		prevClock: prevClock, prevClockSet: prevClockSet,
		prevPartitionWM: prevPartitionWM,
	})
	return merged, nil
}

// Merged 返回当前合并水位；可被并发调用，并发读到的值单调不减。
func (m *Merger) Merged() Watermark {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.merged
}

// Now 返回最近一次注入的处理时间；时钟尚未注入时返回 0 与 false。
func (m *Merger) Now() (Time, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.now, m.clockSet
}

// Snapshot 是只读状态快照，供查询与日志使用。
type Snapshot struct {
	Now           Time
	ClockSet      bool
	Merged        Watermark
	IdleThreshold Time
	Partitions    []PartitionStatus
}

// PartitionStatus 是单个分区在快照中的状态。
type PartitionStatus struct {
	Partition    int
	Watermark    Watermark
	LastActive   Time
	HasWatermark bool
	Idle         bool
}

// Snapshot 返回当前状态的一致副本，可与写操作并发调用。
func (m *Merger) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snap := Snapshot{
		Now:           m.now,
		ClockSet:      m.clockSet,
		Merged:        m.merged,
		IdleThreshold: m.idleThreshold,
		Partitions:    make([]PartitionStatus, len(m.partitions)),
	}
	for i := range m.partitions {
		p := &m.partitions[i]
		snap.Partitions[i] = PartitionStatus{
			Partition:    i,
			Watermark:    p.watermark,
			LastActive:   p.lastActive,
			HasWatermark: p.hasWatermark,
			Idle:         m.idleLocked(p),
		}
	}
	return snap
}

// idleLocked 判定分区是否空闲：已上报过水位，且距最后活跃时间已达到阈值
// （elapsed == threshold 恰好相等时也算空闲）。调用方需持有 m.mu。
func (m *Merger) idleLocked(p *partitionState) bool {
	if !p.hasWatermark {
		return false
	}
	return m.now-p.lastActive >= m.idleThreshold
}

// decision 记录一次重算的输入、候选与推进依据，供日志使用。
type decision struct {
	op        string
	partition int
	at        Time
	reported  Watermark
	hasReport bool

	prevClock       Time
	prevClockSet    bool
	prevPartitionWM Watermark

	elapsed      []Time
	idle         []bool
	candidates   []int
	minCandidate Watermark
	hasCandidate bool

	prevMerged Watermark
	newMerged  Watermark
	advanced   bool
	reason     string
}

// recomputeLocked 依据当前状态重算合并水位：
// 候选分区 = 已上报且非空闲的分区；候选非空时 merged 前进到 max(merged, min(候选水位))，
// 候选为空（全部空闲或均未上报）时 merged 保持不变。调用方需持有 m.mu。
func (m *Merger) recomputeLocked(d decision) Watermark {
	d.prevMerged = m.merged
	d.elapsed = make([]Time, len(m.partitions))
	d.idle = make([]bool, len(m.partitions))

	for i := range m.partitions {
		p := &m.partitions[i]
		if p.hasWatermark {
			d.elapsed[i] = m.now - p.lastActive
		}
		d.idle[i] = m.idleLocked(p)
		if p.hasWatermark && !d.idle[i] {
			if !d.hasCandidate || p.watermark < d.minCandidate {
				d.minCandidate = p.watermark
				d.hasCandidate = true
			}
			d.candidates = append(d.candidates, i)
		}
	}

	if !d.hasCandidate {
		d.newMerged = m.merged
		d.advanced = false
		d.reason = "no active partition: merged watermark held"
	} else if d.minCandidate > m.merged {
		d.newMerged = d.minCandidate
		d.advanced = true
		d.reason = "advanced to minimum watermark of active partitions"
	} else {
		d.newMerged = m.merged
		d.advanced = false
		d.reason = "minimum active watermark not ahead of merged: held"
	}

	m.merged = d.newMerged
	m.logDecision(d)
	return m.merged
}
