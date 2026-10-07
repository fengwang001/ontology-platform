package ontology

// DecisionCode 标识创建/删除仲裁的判定结论。调用方通过 Code 区分失败类别，
// 任何场景下都不得把多个类别合并成同一个错误。
type DecisionCode string

const (
	// CodeObjectNotFound 所引用的对象实例不存在或已被逻辑删除。
	CodeObjectNotFound DecisionCode = "object_not_found"
	// CodeLinkTypeNotFound 引用了未注册的链接类型。
	CodeLinkTypeNotFound DecisionCode = "link_type_not_found"
	// CodeLinkTypeNotAllowed 链接类型不允许在这两个对象类型之间按该方向建立链接。
	CodeLinkTypeNotAllowed DecisionCode = "link_type_not_allowed"
	// CodeDuplicateLink 区分属性组合与在库链接完全相同，判为重复声明。
	CodeDuplicateLink DecisionCode = "duplicate_link"
	// CodeCardinalityFull 目标方向已登记链接数达到该方向声明的基数上限。
	CodeCardinalityFull DecisionCode = "cardinality_full"
	// CodeLinkNotFound 删除/注解的目标链接不在库（含已撤销）。
	CodeLinkNotFound DecisionCode = "link_not_found"
	// CodeAccepted 请求被接受并生效（用于审计记录）。
	CodeAccepted DecisionCode = "accepted"
)

// DecisionError 携带稳定的机器可读码、面向人的说明以及判定依据。
// Basis 中每一项对应审计记录里的一条“判定依据”，便于事后核对。
type DecisionError struct {
	code  DecisionCode
	msg   string
	basis []string
}

func (e *DecisionError) Error() string      { return e.msg }
func (e *DecisionError) Code() DecisionCode { return e.code }
func (e *DecisionError) Basis() []string    { return append([]string(nil), e.basis...) }

var (
	ErrObjectNotFound     = &DecisionError{code: CodeObjectNotFound, msg: "ontology: referenced object instance does not exist or is logically deleted"}
	ErrLinkTypeNotFound   = &DecisionError{code: CodeLinkTypeNotFound, msg: "ontology: link type is not registered"}
	ErrLinkTypeNotAllowed = &DecisionError{code: CodeLinkTypeNotAllowed, msg: "ontology: link type does not permit this direction between the given object types"}
	ErrDuplicateLink      = &DecisionError{code: CodeDuplicateLink, msg: "ontology: link with the same discriminator attribute tuple already exists"}
	ErrCardinalityFull    = &DecisionError{code: CodeCardinalityFull, msg: "ontology: cardinality cap of the requested direction is reached"}
	ErrLinkNotFound       = &DecisionError{code: CodeLinkNotFound, msg: "ontology: link does not exist or has been revoked"}
)

// AsDecisionError 提取仲裁错误。哨兵变量是只读的；带依据的错误通过
// withBasis 派生，不会改写哨兵本身。
func AsDecisionError(err error) (*DecisionError, bool) {
	if err == nil {
		return nil, false
	}
	e, ok := err.(*DecisionError)
	return e, ok
}

func withBasis(sentinel *DecisionError, basis ...string) *DecisionError {
	return &DecisionError{code: sentinel.code, msg: sentinel.msg, basis: basis}
}
