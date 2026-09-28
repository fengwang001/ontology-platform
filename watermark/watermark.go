package watermark

import (
	"fmt"
	"log/slog"
	"sync"
)

// Merger 多分区输入合并水位线组件。
//
// 所有写操作（Report、AdvanceClock）与读操作均通过内部互斥保护，
// 可被并发使用。处理时间由调用方注入，单位由调用方约定（毫秒、微秒或
// 任意单调递增的刻度均可），水位、最后活跃时间与空闲阈值使用同一套整数刻度。
type Merger struct {
	mu sync.RWMutex

	numPartitions int
	idleThreshold int64 // 分区距最后活跃多久后视为空闲（边界相等即空闲）

	clock      int64   // 调用方最近一次注入的处理时间
	watermarks []int64 // 各分区最近上报的水位
	reported   []bool  // 各分区是否上报过
	lastActive []int64 // 各分区最后活跃（上报）时的处理时间

	merged      int64 // 当前合并水位
	initialized bool  // merged 是否已被候选值初始化过

	logger *slog.Logger
}

// New 创建一个 numPartitions 分区的合并器，分区空闲阈值为 idleThreshold。
// 分区数必须为正，空闲阈值必须为正，否则返回包装了 ErrInvalidArgument 的错误。
func New(numPartitions int, idleThreshold int64) (*Merger, error) {
	if numPartitions <= 0 {
		return nil, fmt.Errorf("%w: numPartitions must be positive, got %d",
			ErrInvalidArgument, numPartitions)
	}
	if idleThreshold <= 0 {
		return nil, fmt.Errorf("%w: idleThreshold must be positive, got %d",
			ErrInvalidArgument, idleThreshold)
	}
	return &Merger{
		numPartitions: numPartitions,
		idleThreshold: idleThreshold,
		watermarks:    make([]int64, numPartitions),
		reported:      make([]bool, numPartitions),
		lastActive:    make([]int64, numPartitions),
		logger:        slog.Default(),
	}, nil
}

// Report 上报 partition 在处理时间 now 的分区水位 watermark。
//
// 拒绝情形（按判定顺序），任一拒绝都不会改变任何状态：
//   - partition 越界：包装 ErrPartitionOutOfRange；
//   - now 小于已注入的处理时间：包装 ErrClockBackward；
//   - watermark 小于该分区已上报水位：包装 ErrWatermarkBackward。
//
// now 与当前处理时间相等、watermark 与分区当前水位相等均允许（只进不退）。
func (m *Merger) Report(partition int, watermark int64, now int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if partition < 0 || partition >= m.numPartitions {
		err := fmt.Errorf("%w: partition %d not in [0,%d)",
			ErrPartitionOutOfRange, partition, m.numPartitions)
		m.logger.Warn("watermark report rejected",
			"op", "report", "partition", partition,
			"watermark", watermark, "now", now, "reason", err)
		return err
	}
	if now < m.clock {
		err := fmt.Errorf("%w: now=%d is before current processing time %d",
			ErrClockBackward, now, m.clock)
		m.logger.Warn("watermark report rejected",
			"op", "report", "partition", partition,
			"watermark", watermark, "now", now, "reason", err)
		return err
	}
	if m.reported[partition] && watermark < m.watermarks[partition] {
		err := fmt.Errorf("%w: partition %d watermark %d is before current %d",
			ErrWatermarkBackward, partition, watermark, m.watermarks[partition])
		m.logger.Warn("watermark report rejected",
			"op", "report", "partition", partition,
			"watermark", watermark, "now", now, "reason", err)
		return err
	}

	prevMerged, prevInitialized := m.merged, m.initialized

	m.clock = now
	m.watermarks[partition] = watermark
	m.reported[partition] = true
	m.lastActive[partition] = now

	candidateMin, found, idle := m.recomputeLocked()
	m.logDecision("report", partition, watermark, now,
		prevMerged, prevInitialized, candidateMin, found, idle)
	return nil
}

// AdvanceClock 仅推进处理时间（无新数据到达），用于驱动空闲判定。
// 当时钟推进使停滞分区跨过空闲阈值时，这些分区被移出候选集，
// 合并水位可能因此越过它们前进。now 小于当前处理时间时返回
// 包装了 ErrClockBackward 的错误且不改变状态。
func (m *Merger) AdvanceClock(now int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if now < m.clock {
		err := fmt.Errorf("%w: now=%d is before current processing time %d",
			ErrClockBackward, now, m.clock)
		m.logger.Warn("watermark clock advance rejected",
			"op", "advance_clock", "now", now, "reason", err)
		return err
	}

	prevMerged, prevInitialized := m.merged, m.initialized
	m.clock = now

	candidateMin, found, idle := m.recomputeLocked()
	m.logDecision("advance_clock", -1, 0, now,
		prevMerged, prevInitialized, candidateMin, found, idle)
	return nil
}

// Watermark 返回当前合并水位，可并发读取。在任何候选分区出现之前返回 0；
// 一旦推进过，结果相对历史调用单调不减。
func (m *Merger) Watermark() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.merged
}

// PartitionSnapshot 描述单个分区的观测状态，用于日志与测试。
type PartitionSnapshot struct {
	Partition  int   // 分区编号
	Watermark  int64 // 分区最近上报水位（未上报时为 0）
	Reported   bool  // 是否上报过
	LastActive int64 // 最后活跃（上报）时的处理时间
	Idle       bool  // 当前是否不参与合并（未上报或已达空闲阈值）
}

// Snapshot 返回处理时间、合并水位与各分区状态的一致快照（加读锁获取），
// 各元素与日志中的判定依据一一对应，便于排查。
func (m *Merger) Snapshot() (clock int64, merged int64, partitions []PartitionSnapshot) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	partitions = make([]PartitionSnapshot, m.numPartitions)
	for i := range partitions {
		partitions[i] = PartitionSnapshot{
			Partition:  i,
			Watermark:  m.watermarks[i],
			Reported:   m.reported[i],
			LastActive: m.lastActive[i],
			Idle:       !m.reported[i] || m.clock-m.lastActive[i] >= m.idleThreshold,
		}
	}
	return m.clock, m.merged, partitions
}

// recomputeLocked 依据当前时钟重算合并水位并更新 m.merged（调用方须持写锁）。
//
// 候选集为“已上报且未空闲”的分区：
//   - 候选集为空（全部空闲，或尚无已上报的非空闲分区）：合并水位保持不变；
//   - 否则候选值为候选集水位最小值，合并水位前进到 max(当前值, 候选值)，
//     因此空闲分区带着低值恢复时，只会参与取小而不会把合并水位拉退。
//
// 返回候选最小值、候选集是否非空以及每个分区的空闲掩码，供日志使用。
func (m *Merger) recomputeLocked() (candidateMin int64, found bool, idle []bool) {
	idle = make([]bool, m.numPartitions)
	for i := 0; i < m.numPartitions; i++ {
		if !m.reported[i] {
			// 从未上报的分区没有水位，天然不参与候选。
			idle[i] = true
			continue
		}
		// 不变量：clock 只进不退、lastActive 取历史 clock 值，故差值非负。
		idle[i] = m.clock-m.lastActive[i] >= m.idleThreshold
		if idle[i] {
			continue
		}
		if !found || m.watermarks[i] < candidateMin {
			candidateMin = m.watermarks[i]
			found = true
		}
	}

	if found {
		if !m.initialized || candidateMin > m.merged {
			m.merged = candidateMin
			m.initialized = true
		}
	}
	return candidateMin, found, idle
}

// logDecision 记录一次输入后的完整判定依据：输入、各分区空闲状态、
// 候选最小值，以及合并水位是前进还是保持。partition 为 -1 表示纯时钟推进。
func (m *Merger) logDecision(op string, partition int, inputWM int64, now int64,
	prevMerged int64, prevInit bool, candidateMin int64, found bool, idle []bool) {

	active := make([]int, 0, m.numPartitions)
	idles := make([]int, 0, m.numPartitions)
	unreported := make([]int, 0, m.numPartitions)
	for i := range idle {
		switch {
		case !m.reported[i]:
			unreported = append(unreported, i)
		case idle[i]:
			idles = append(idles, i)
		default:
			active = append(active, i)
		}
	}

	args := []any{
		"op", op,
		"now", now,
		"active_partitions", active,
		"idle_partitions", idles,
		"unreported_partitions", unreported,
		"candidate_present", found,
		"candidate_min", candidateMin,
		"merged_before", prevMerged,
		"merged_before_initialized", prevInit,
		"merged", m.merged,
	}
	if partition >= 0 {
		args = append(args, "partition", partition, "input_watermark", inputWM)
	}
	m.logger.Info("watermark merged", args...)
}
