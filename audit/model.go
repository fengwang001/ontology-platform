package audit

// Kind 标识审计记录的类型。
type Kind int

const (
	// KindAction 表示动作成功提交。
	KindAction Kind = iota + 1
	// KindRollback 表示动作执行后整体回退（仅记录“发生过一次尝试”）。
	KindRollback
	// KindCorrection 表示对某条已提交动作记录的订正。
	KindCorrection
)

func (k Kind) String() string {
	switch k {
	case KindAction:
		return "ACTION"
	case KindRollback:
		return "ROLLBACK"
	case KindCorrection:
		return "CORRECTION"
	default:
		return "UNKNOWN"
	}
}

// Change 是单个实例在一条审计记录中的变更前后值。
type Change struct {
	Instance string
	Before   string
	After    string
}

// Record 是审计序列中的一条不可变记录。
type Record struct {
	Seq       int64
	Kind      Kind
	ActionID  string
	Changes   []Change
	TargetSeq int64 // 仅订正记录：被订正动作记录的序号
	PrevHash  []byte
	Hash      []byte
}

// Write 是一次动作对单个实例的写入意图。
type Write struct {
	Instance string
	Value    string
}

// Action 描述一次动作执行请求。
type Action struct {
	ActionID string
	TypeName string
	Writes   []Write
}

// ReplayEvent 表示区间重放中按序号重建出的一次状态差异。
type ReplayEvent struct {
	Seq      int64
	Kind     Kind
	ActionID string
	Changes  []Change
}

// ReplayResult 是一次区间重放的结果。
type ReplayResult struct {
	FromSeq int64
	ToSeq   int64
	// StateFrom 为 FromSeq 时刻（即区间起点之前）的对象状态。
	StateFrom map[string]string
	// StateTo 为 ToSeq 时刻（即区间终点之后）的对象状态。
	StateTo map[string]string
	// Events 为区间内导致状态差异的全部变更，按序号排列。
	Events []ReplayEvent
}
