package ontology

import "fmt"

// ErrorKind 区分动作被拒绝的原因类别。
// 数值越小优先级越高；引擎按固定且唯一的优先顺序汇报：
//  1. ErrInvalidParams      动作声明的参数本身不合法
//  2. ErrDepthExceeded      传播深度超过声明上限
//  3. ErrTargetInvisible    主体对动作的直接目标实例不可见
//  4. ErrCascadeInvisible   级联触及的实例不可见且当前模式要求整体拒绝
type ErrorKind int

const (
	ErrInvalidParams ErrorKind = iota + 1
	ErrDepthExceeded
	ErrTargetInvisible
	ErrCascadeInvisible
)

// ActionError 是动作被拒绝时返回的错误。
// 为避免泄露不可见实例的存在，ErrCascadeInvisible 不携带任何实例标识。
type ActionError struct {
	Kind   ErrorKind
	Target InstanceID // 仅 ErrInvalidParams / ErrTargetInvisible 可能携带
	Detail string
}

func (e *ActionError) Error() string {
	if e.Target != "" {
		return fmt.Sprintf("ontology: %s (target %s): %s", e.Kind, e.Target, e.Detail)
	}
	return fmt.Sprintf("ontology: %s: %s", e.Kind, e.Detail)
}

func (k ErrorKind) String() string {
	switch k {
	case 0:
		return "none"
	case ErrInvalidParams:
		return "invalid-params"
	case ErrDepthExceeded:
		return "depth-exceeded"
	case ErrTargetInvisible:
		return "target-invisible"
	case ErrCascadeInvisible:
		return "cascade-invisible"
	default:
		return "unknown"
	}
}
