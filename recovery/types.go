package recovery

import "errors"

// State 是对象状态在本协调器中的抽象表示。
// 协调器不解释状态语义，只负责在校验通过后按动作依次变换。
type State = string

// Checksum 是记录自带的校验信息。
type Checksum = string

// Version 标识快照时刻与动作日志起点，必须衔接方可重放。
type Version = string

// ObjectID 标识对象。
type ObjectID = string

// ActionID 标识一条动作记录。
type ActionID = string

// SnapshotRecord 是快照中单个对象的状态记录。
// 记录不存在（快照里没有该 key）与存在但校验失败（损坏/不可读）必须可区分。
type SnapshotRecord struct {
	ObjectID ObjectID
	State    State
	Checksum Checksum
}

// Snapshot 是某一时刻的快照。
type Snapshot struct {
	Version Version
	Records []SnapshotRecord
}

// Effect 是一条动作对单个对象产生的效果。
// HasStart=true 时 Start 给出该对象在本动作中的完整起点状态，
// 即使对象此前状态完全未知，也可由 (Start, Change) 锚定并重建。
// HasStart=false 时只有变更 Change，必须基于一个已知状态才能应用。
type Effect struct {
	HasStart bool
	Start    State
	Change   State
}

// Action 是动作记录的有效负载（校验通过后的完整内容）。
type Action struct {
	ActionID ActionID
	Base     Version // 该动作日志的起点版本，必须与快照版本衔接
	Effects  map[ObjectID]Effect
}

// ActionRecord 是日志中的一条动作记录（含自带校验信息）。
// 记录一旦损坏（无法解析或校验失败），整条视为不可用，
// 不得只采信其中部分可解析字段。
type ActionRecord struct {
	ActionID ActionID
	Base     Version
	Effects  map[ObjectID]Effect
	Checksum Checksum
}

// Log 是快照时刻之后的动作日志。
type Log struct {
	Base    Version // 日志起点版本
	Records []ActionRecord
}

// ObjectSource 是对象最终状态的来源分类。
type ObjectSource int

const (
	// SourceSnapshot 直接来自完好快照。
	SourceSnapshot ObjectSource = iota + 1
	// SourceReplay 快照不可读（或快照中不存在），由完整动作记录重建。
	SourceReplay
	// SourceUnreadable 快照不可读且始终没有任何完整动作提供起点，仍不可读。
	SourceUnreadable
)

func (s ObjectSource) String() string {
	switch s {
	case SourceSnapshot:
		return "snapshot"
	case SourceReplay:
		return "replay"
	case SourceUnreadable:
		return "unreadable"
	default:
		return "unknown"
	}
}

// ObjectStatus 描述对象在快照层面的状态。
type ObjectStatus int

const (
	// StatusAbsent 对象在本次快照中不存在（区别于不可读）。
	StatusAbsent ObjectStatus = iota + 1
	// StatusValid 快照记录完好。
	StatusValid
	// StatusCorrupt 快照记录损坏，对象在快照时刻状态不可读。
	StatusCorrupt
)

func (s ObjectStatus) String() string {
	switch s {
	case StatusAbsent:
		return "absent"
	case StatusValid:
		return "valid"
	case StatusCorrupt:
		return "corrupt"
	default:
		return "unknown"
	}
}

// ObjectReport 是对象级别的修复结果。
type ObjectReport struct {
	ObjectID    ObjectID
	Snapshot    ObjectStatus // 快照层面：不存在 / 完好 / 损坏（不可读）
	Source      ObjectSource // 最终状态来源：快照 / 动作重建 / 仍不可读
	FinalState  State        // SourceUnreadable 时为空
	AnchorIndex int          // 锚定该对象的动作在有效动作序列中的下标；-1 表示无锚点
}

// Report 是一次修复的完整对象级报告。
type Report struct {
	SnapshotVersion Version
	LogBase         Version
	Objects         map[ObjectID]ObjectReport
	// CorruptObjects 列出快照记录损坏（对象级不可读）的对象。
	CorruptObjects []ObjectID
	// CorruptActions 列出校验失败、被整条丢弃的动作。
	CorruptActions []ActionID
}

// DecisionLogEntry 记录一次判定的输入、输出与依据，供审计与测试验证。
type DecisionLogEntry struct {
	Stage  string
	Input  string
	Output string
	Reason string
}

// 可区分的错误类别：彼此不得混报。
var (
	// ErrSnapshotCorrupt 快照记录损坏（对象级），损坏对象清单见详情。
	ErrSnapshotCorrupt = errors.New("snapshot record corrupt (object-level)")
	// ErrActionCorrupt 动作日志记录损坏，损坏动作清单见详情。
	ErrActionCorrupt = errors.New("action log record corrupt")
	// ErrVersionMismatch 快照与日志版本不衔接（日志起点与快照时刻不匹配）。
	ErrVersionMismatch = errors.New("snapshot and log versions do not line up")
	// ErrObjectOutOfCoverage 请求对象不在本次快照与日志的覆盖范围内。
	ErrObjectOutOfCoverage = errors.New("requested object is outside snapshot/log coverage")
)

// CoverageError 携带超出覆盖范围的对象清单。
type CoverageError struct {
	ObjectIDs []ObjectID
}

func (e *CoverageError) Error() string {
	return ErrObjectOutOfCoverage.Error()
}

func (e *CoverageError) Unwrap() error { return ErrObjectOutOfCoverage }
