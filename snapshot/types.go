package snapshot

import "sort"

// Time 是调用方提供的逻辑时刻，单调不减（取等合法）。
type Time int64

// Phase 是组的生命周期阶段。快照自身的三阶段为：冻结中、已冻结、已结束；
// “已结束”体现为组回到空闲（提交产生记录，中止不产生）。
type Phase int

const (
	PhaseIdle     Phase = iota // 空闲：无进行中的快照
	PhaseFreezing              // 冻结中：逐卷确认，未确认卷照常应用写入
	PhaseFrozen                // 已冻结：全部卷写入排队，等待提交或中止
)

var phaseNames = map[Phase]string{
	PhaseIdle:     "idle",
	PhaseFreezing: "freezing",
	PhaseFrozen:   "frozen",
}

func (p Phase) String() string {
	if name, ok := phaseNames[p]; ok {
		return name
	}
	return "unknown"
}

const (
	// MinGroupSize 是一个组允许的最少卷数。
	MinGroupSize = 2
	// MaxGroupSize 是一个组允许的最多卷数。
	MaxGroupSize = 16
)

// Config 是协调服务的配置。
type Config struct {
	// MaxHoldDuration 是已冻结阶段的最长保持时长：
	// 时刻严格大于 快照点+MaxHoldDuration 仍未提交则自动中止。
	MaxHoldDuration Time
}

// WriteOutcome 区分一次写入被直接应用还是被排队。
type WriteOutcome int

const (
	WriteApplied WriteOutcome = iota
	WriteQueued
)

// WriteResult 是一次被接受写入的结果。
type WriteResult struct {
	Outcome WriteOutcome
	// Seq 仅在 Outcome == WriteApplied 时有效，是分配给该写入的写序号。
	Seq uint64
}

// ConfirmResult 是一次被接受的冻结确认的结果。
type ConfirmResult struct {
	// Completed 为 true 表示这是最后一个确认，快照此刻进入已冻结阶段。
	Completed bool
	// SnapshotPoint 仅在 Completed 为 true 时有效，即快照点时刻。
	SnapshotPoint Time
}

// SnapshotRecord 是提交时生成的快照记录。
type SnapshotRecord struct {
	GroupID string
	// Index 是该组快照记录的序号，从 0 开始递增。
	Index int
	// Point 是快照点，即最后一个确认发生的时刻。
	Point Time
	// CommittedAt 是提交发生的时刻。
	CommittedAt Time
	// Cutoffs 是快照点各卷已应用的最大写序号。
	Cutoffs map[string]uint64
}

// VolumeCutoff 是 (卷, 截止写序号) 对，用于确定性排序输出。
type VolumeCutoff struct {
	VolumeID string
	Seq      uint64
}

// SortedCutoffs 返回按卷标识排序的截止点列表，便于确定性比较与打印。
func (r SnapshotRecord) SortedCutoffs() []VolumeCutoff {
	out := make([]VolumeCutoff, 0, len(r.Cutoffs))
	for id, seq := range r.Cutoffs {
		out = append(out, VolumeCutoff{VolumeID: id, Seq: seq})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].VolumeID < out[j].VolumeID })
	return out
}

// VolumeView 是卷状态的只读视图。
type VolumeView struct {
	ID      string
	Seq     uint64
	GroupID string // 空串表示不属于任何组
	// Enqueueing 为 true 表示此刻新到写入会被排队而非直接应用。
	Enqueueing bool
	// Queue 是按到达次序排队的写入载荷副本。
	Queue []string
}

// GroupView 是组状态的只读视图。
type GroupView struct {
	ID      string
	Phase   Phase
	Members []string // 按卷标识排序
	// Confirmed 是冻结中阶段已确认卷（按卷标识排序）。
	Confirmed []string
	// RemainingConfirmations 是冻结中阶段尚未确认的卷数。
	RemainingConfirmations int
	FreezeDeadline         Time
	SnapshotPoint          Time
	// Cutoffs 在已冻结阶段为各卷截止点（等于快照点各卷写序号）。
	Cutoffs map[string]uint64
	// Records 是该组已提交的快照记录，按生成次序排列。
	Records []SnapshotRecord
}
