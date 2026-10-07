// Package ontology 提供本体平台的核心领域模型与动作执行引擎。
//
// 动作（Action）的执行被严格划分为前置校验阶段与后置校验阶段：
//   - 前置校验只能读取动作开始执行前已持久化（已提交）的对象与链接状态；
//   - 后置校验只能基于本次执行计划写入的最终结果（统一快照视角）作出判断；
//   - 两个阶段使用不同的输入类型，结构上保证互不污染对方的判断依据。
package ontology

// ObjectID 是对象的唯一标识。
type ObjectID string

// LinkID 是链接的唯一标识。
type LinkID string

// Object 是一个本体对象。Props 中的值必须为标量（int64/float64/string/bool），
// 以便快照能够安全地进行浅拷贝。
type Object struct {
	ID      ObjectID
	Type    string
	Props   map[string]any
	Version int64
}

// Link 是两个对象之间的有向链接。
type Link struct {
	ID      LinkID
	Type    string
	From    ObjectID
	To      ObjectID
	Version int64
}

// Phase 标识校验所处的阶段。
type Phase int

const (
	// PhasePre 前置校验阶段。
	PhasePre Phase = iota
	// PhasePost 后置校验阶段。
	PhasePost
)

func (p Phase) String() string {
	if p == PhasePre {
		return "pre"
	}
	return "post"
}

// RejectCategory 是动作调用被拒绝的原因类别。四种类别必须可区分地暴露。
type RejectCategory int

const (
	// RejectPrecondition 前置条件不通过。
	RejectPrecondition RejectCategory = iota
	// RejectPostcondition 后置条件不通过。
	RejectPostcondition
	// RejectDeclarationContradiction 动作声明本身自相矛盾，无法执行。
	RejectDeclarationContradiction
	// RejectConcurrentInvalidation 目标对象在执行期间被并发撤销/修改。
	RejectConcurrentInvalidation
)

func (c RejectCategory) String() string {
	switch c {
	case RejectPrecondition:
		return "precondition_failed"
	case RejectPostcondition:
		return "postcondition_failed"
	case RejectDeclarationContradiction:
		return "declaration_contradiction"
	case RejectConcurrentInvalidation:
		return "concurrent_invalidation"
	}
	return "unknown"
}

// Call 是一次动作调用。
type Call struct {
	// ID 由调用方提供；为空时由执行器分配（调用 ID 不属于对象状态，
	// 分配它不会消耗任何对象版本号或历史序号）。
	ID string
	// ActionType 是动作类型标识。
	ActionType string
	// Params 是动作参数。
	Params map[string]any
	// Targets 是动作声明的主要目标对象，必须在执行开始时已经存在，
	// 否则按 RejectConcurrentInvalidation 处理（该优先级对相同输入稳定）。
	Targets []ObjectID
}

// Rejection 描述一次被拒绝的执行。
type Rejection struct {
	Category RejectCategory
	// 前置校验失败时，包含全部不通过的条件 ID（不对称性要求：前置必须穷举）。
	FailedPreconditions []string
	// 后置校验失败时，只包含导致本次放弃的决定性条件 ID（不对称性要求：后置不穷举）。
	DecisivePostcondition string
	Detail                string
}

// Status 是执行结果状态。
type Status int

const (
	// StatusAccepted 调用被接受并已提交。
	StatusAccepted Status = iota
	// StatusRejected 调用被四类原因之一拒绝。
	StatusRejected
	// StatusError 调用因动作逻辑错误（如 Apply 返回错误、未知动作类型）失败，
	// 不属于四类拒绝类别。
	StatusError
)

// Result 是一次动作执行的结果。
type Result struct {
	CallID string
	Status Status
	Reject *Rejection
	// CommitSeq 仅在 StatusAccepted 时有意义，是全局提交序号，
	// 给出了被接受调用集合的一个全序。
	CommitSeq int64
	// Err 仅在 StatusError 时有意义。
	Err error
}

// Accepted 报告调用是否被接受。
func (r Result) Accepted() bool { return r.Status == StatusAccepted }
