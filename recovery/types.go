package recovery

// ObjectID 是对象标识。
type ObjectID string

// Version 标识快照基线与动作日志起点的衔接关系。
// 快照记录的是 [BaseVersion, BaseVersion) 之前的状态；
// 动作日志必须恰好从 BaseVersion 开始（StartVersion == BaseVersion）。
type Version struct {
	Epoch int
	Seq   int64
}

// Equals 判断两个版本是否相等。
func (v Version) Equals(other Version) bool {
	return v.Epoch == other.Epoch && v.Seq == other.Seq
}

// Before 判断 v 是否严格早于 other。
func (v Version) Before(other Version) bool {
	if v.Epoch != other.Epoch {
		return v.Epoch < other.Epoch
	}
	return v.Seq < other.Seq
}

// Next 返回紧接 v 的下一个版本。
func (v Version) Next() Version {
	return Version{Epoch: v.Epoch, Seq: v.Seq + 1}
}

// State 是对象状态。空值表示墓碑（对象不存在 / 已删除）。
// 协调器本身不解释状态语义，只负责状态字符串的衔接与来源判定。
type State string

// ObjectEffect 是一条动作对单个对象产生的影响。
//
// Start 非空时为“起点状态（锚点）”：若该对象在动作之前状态未知，
// 可以以此为起点应用本动作；Start 为空时表示本动作不提供锚点，
// 对象在动作之前必须已经有已知状态，否则整条动作因原子性被整体放弃。
type ObjectEffect struct {
	Start  *State
	Change State
}

// Action 是一条完整（逻辑层、未损坏）的动作记录。
type Action struct {
	Seq     int
	Version Version
	Effects map[ObjectID]ObjectEffect
}

// ObjectSnapshot 是单个对象在快照时刻的记录。
type ObjectSnapshot struct {
	ID     ObjectID
	Exists bool // false 表示对象在快照时刻不存在（墓碑，可与不可读区分）
	State  State
}

// Snapshot 是一份快照（逻辑层）。
type Snapshot struct {
	BaseVersion Version
	Objects     []ObjectSnapshot
}

// Source 是对象最终状态的来源分类。
type Source string

const (
	// SourceSnapshot 直接来自完好快照记录（之后的动作只是沿可读链推进）。
	SourceSnapshot Source = "snapshot"
	// SourceRebuilt 经动作日志重建（快照记录不可读，由后续某条完整动作提供锚点）。
	SourceRebuilt Source = "rebuilt"
	// SourceUnknown 仍判定为不可读（快照不可读且无任何已应用动作提供起点）。
	SourceUnknown Source = "unknown"
)

// ObjectReport 是单个对象的修复结果。
type ObjectReport struct {
	Object ObjectID
	Exists bool
	State  State
	Source Source
	// SnapshotUnreadable 为 true 表示该对象的快照记录损坏、快照时刻状态不可读。
	SnapshotUnreadable bool
	// AnchorSeq 是重建起点动作的序号；仅 Source==SourceRebuilt 时有意义。
	AnchorSeq int
}

// ActionOutcome 是一条动作记录在恢复过程中的判定结果。
type ActionOutcome string

const (
	// ActionCorrupt 记录损坏：整条动作不可用，任何字段都不采信。
	ActionCorrupt ActionOutcome = "corrupt"
	// ActionBlocked 记录完整，但原子性前置条件不满足，整体不应用。
	ActionBlocked ActionOutcome = "blocked"
	// ActionApplied 记录完整且已整体应用到其涉及的全部对象。
	ActionApplied ActionOutcome = "applied"
)

// ActionJudgment 记录一条动作记录的判定依据（供日志与审计使用）。
type ActionJudgment struct {
	Index           int
	Seq             int
	Outcome         ActionOutcome
	BlockingObjects []ObjectID // Outcome==ActionBlocked 时，起点未知的对象
	Reason          string
}

// RepairReport 是一次修复/查询请求的完整报告。
type RepairReport struct {
	SnapshotBase Version
	LogStart     Version
	Objects      map[ObjectID]ObjectReport
	Judgments    []ActionJudgment
	// SnapshotCorruptions 按对象列出快照记录损坏。
	SnapshotCorruptions map[ObjectID]string
}

// DecisionLogger 记录每次判定的输入、输出与依据。
type DecisionLogger interface {
	Log(entry DecisionLogEntry)
}

// DecisionLogEntry 是一条判定日志。
type DecisionLogEntry struct {
	Stage  string // "snapshot_load" | "version_check" | "action_judge" | "request"
	Input  string
	Output string
	Reason string
}
