// Package savepoint 实现保存点恢复准入器：在新作业图与保存点之间
// 按算子标识与来源映射匹配状态、判定兼容性，保证恢复要么完整可用、
// 要么零副作用地被拒绝。
package savepoint

// StateKind 状态项种类。
type StateKind string

const (
	KindValue StateKind = "value"
	KindList  StateKind = "list"
	KindMap   StateKind = "map"
)

// ValueType 状态项值类型。
type ValueType string

const (
	TypeInt    ValueType = "int"
	TypeLong   ValueType = "long"
	TypeString ValueType = "string"
)

// StateItem 具名状态项定义。
type StateItem struct {
	Name string
	Kind StateKind
	Type ValueType
}

// SavepointOperator 保存点中的算子快照。
type SavepointOperator struct {
	ID             string
	MaxParallelism int
	States         []StateItem
}

// Savepoint 保存点，登记后不可变。
type Savepoint struct {
	ID        string
	Operators []SavepointOperator
}

// JobOperator 新作业图中的算子。
type JobOperator struct {
	ID string
	// Parallelism 实际并行度 p，必须为正且不超过最大并行度。
	Parallelism int
	// MaxParallelism 可选；缺省时承接者继承保存点的 m，新增算子默认 128。
	MaxParallelism *int
	// SourceID 可选来源标识；声明后承接保存点中该标识的算子，
	// 否则承接与自身同名的保存点算子。
	SourceID *string
	// States 新图该算子声明的具名状态项。
	States []StateItem
}

// JobGraph 新作业图。
type JobGraph struct {
	Operators []JobOperator
}

// Action 算子级恢复动作。
type Action string

const (
	ActionRestoreDirect Action = "restore-direct"
	ActionRestoreWiden  Action = "restore-widen"
	ActionStartEmpty    Action = "start-empty"
)

// OperatorPlan 单个新算子的恢复计划。
type OperatorPlan struct {
	OperatorID string
	Action     Action
	// Source 承接的保存点算子标识；新增算子为空。
	Source string
	// MaxParallelism 生效的最大并行度。
	MaxParallelism int
	// ItemActions 各状态项的动作（按名称升序）；新增算子为空。
	ItemActions map[string]Action
}

// Plan 恢复计划，相同输入得到完全相同的输出。
type Plan struct {
	JobID       string
	SavepointID string
	// Operators 按算子标识升序。
	Operators []OperatorPlan
	// DroppedOperators 允许丢弃时未被承接的保存点算子，升序。
	DroppedOperators []string
	// DroppedStateItems 允许丢弃时新图缺失的状态项（"算子.状态项"），升序。
	DroppedStateItems []string
}

// RejectReason 拒绝原因类别，按声明次序判定，只报第一类。
type RejectReason string

const (
	ReasonSavepointNotFound      RejectReason = "savepoint-not-found"
	ReasonJobIDInUse             RejectReason = "job-id-in-use"
	ReasonInvalidGraph           RejectReason = "invalid-graph"
	ReasonDuplicateClaim         RejectReason = "duplicate-claim"
	ReasonMaxParallelismMismatch RejectReason = "max-parallelism-mismatch"
	ReasonUnclaimedOperators     RejectReason = "unclaimed-operators"
	ReasonIncompatibleStateItems RejectReason = "incompatible-state-items"
	ReasonMissingStateItems      RejectReason = "missing-state-items"
)

// RejectError 恢复被拒绝的错误，Objects 为同类全部对象（升序）。
type RejectError struct {
	Reason  RejectReason
	Objects []string
}

func (e *RejectError) Error() string {
	out := string(e.Reason)
	for i, s := range e.Objects {
		if i == 0 {
			out += ": "
		} else {
			out += ", "
		}
		out += s
	}
	return out
}
