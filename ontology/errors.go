package ontology

// ErrorKind 是错误类别，优先级数字越小越优先（见 RejectXxx 常量）。
type ErrorKind int

const (
	// RejectInvalidDeclaration：动作声明的参数本身不合法（最高优先）。
	RejectInvalidDeclaration ErrorKind = iota + 1
	// RejectDepthExceeded：传播深度超过声明上限。
	RejectDepthExceeded
	// RejectDirectInvisible：主体对直接目标实例不可见。
	RejectDirectInvisible
	// RejectCascadeInvisible：级联触及实例不可见且模式要求整体拒绝。
	RejectCascadeInvisible
	// RejectDenied：合并规则最终判定拒绝。
	RejectDenied
)

// ActionError 携带固定优先顺序的裁决错误。
type ActionError struct {
	Kind ErrorKind
	Msg  string
}

func (e *ActionError) Error() string { return e.Msg }
