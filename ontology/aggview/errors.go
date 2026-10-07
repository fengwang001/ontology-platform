package aggview

// Kind 错误类别（固定优先级：数值越小优先级越高）。
type Kind int

const (
	// KindGroupNotFound 分组实例不存在（或已被删除）。
	KindGroupNotFound Kind = iota + 1
	// KindTypeNotDeclared 被聚合对象类型未声明参与该视图。
	KindTypeNotDeclared
	// KindConflict 归属改变的并发冲突，操作被拒绝且无副作用。
	KindConflict
	// KindUpdateFailed 处理单元内更新失败，整个处理单元回滚。
	KindUpdateFailed
)

// Error 是带类别的强类型错误，调用方按 Kind 区分错误并感知固定优先级。
type Error struct {
	kind Kind
	msg  string
}

func (e *Error) Error() string { return e.msg }

func (e *Error) Kind() Kind { return e.kind }

var (
	errGroupNotFound   = &Error{kind: KindGroupNotFound, msg: "aggview: group instance not found"}
	errTypeNotDeclared = &Error{kind: KindTypeNotDeclared, msg: "aggview: aggregated object type not declared in view"}
	errConflict        = &Error{kind: KindConflict, msg: "aggview: concurrent membership change conflict, operation rejected"}
	errUpdateFailed    = &Error{kind: KindUpdateFailed, msg: "aggview: in-transaction update failed, unit rolled back"}
)

// ErrorKind 提取任意 error 的 aggview 错误类别；非本包错误返回 0。
func ErrorKind(err error) Kind {
	if e, ok := err.(*Error); ok {
		return e.kind
	}
	return 0
}
