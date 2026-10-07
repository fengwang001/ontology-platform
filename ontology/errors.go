package ontology

import "errors"

// 错误类别。调用方须能用 errors.Is 区分下列各类错误。
var (
	// ErrObjectTypeNotFound 表示引用的对象类型不存在。
	ErrObjectTypeNotFound = errors.New("ontology: object type not found")

	// ErrLinkTypeNotFound 表示引用的链接类型不存在。
	ErrLinkTypeNotFound = errors.New("ontology: link type not found")

	// ErrLinkTypeExists 表示新增的链接类型标识已被占用。
	ErrLinkTypeExists = errors.New("ontology: link type already exists")

	// ErrPropagationCycle 表示参与传播的链接类型构成了环路。
	// 该错误与“深度耗尽后传播自然终止”严格区分：后者不是错误。
	ErrPropagationCycle = errors.New("ontology: propagation cycle detected")

	// ErrInvalidDepth 表示传播深度配置非法（负数或超出平台上限）。
	ErrInvalidDepth = errors.New("ontology: invalid propagation depth")

	// ErrConflictingOverrides 表示同一目标对象类型同时被声明了
	// 两种不可调和的覆盖形态。
	ErrConflictingOverrides = errors.New("ontology: conflicting override rules")

	// ErrNoPermission 表示普通意义上的无权限，属于业务结果而非
	// 配置性错误，与上述各类错误区分。
	ErrNoPermission = errors.New("ontology: no permission")
)
