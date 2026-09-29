package spill

// Error 定义溢写事务管理器的可区分错误类别。
type Error struct {
	kind    Kind
	message string
}

// Kind 是错误类别枚举。
type Kind int

const (
	// KindInvalidArgument 非法参数（nil、零值、空负载、非法上限等）。
	KindInvalidArgument Kind = iota + 1
	// KindTxNotFound 事务号不存在（未开启或已结束）。
	KindTxNotFound
	// KindTxDuplicate 事务号重复开启。
	KindTxDuplicate
	// KindSpillFull 溢写存储已满：溢写块数达到上限。
	KindSpillFull
)

func (e *Error) Error() string {
	return e.message
}

// Kind 返回错误类别，便于调用方区分拒绝原因。
func (e *Error) Kind() Kind {
	return e.kind
}

func errInvalidf(format string, args ...any) error {
	return &Error{kind: KindInvalidArgument, message: format}
}

func errNotFoundf(format string, args ...any) error {
	return &Error{kind: KindTxNotFound, message: format}
}

func errDuplicatef(format string, args ...any) error {
	return &Error{kind: KindTxDuplicate, message: format}
}

func errSpillFullf(format string, args ...any) error {
	return &Error{kind: KindSpillFull, message: format}
}
