package matching

import "fmt"

// ErrorKind 对可区分的业务错误进行分类。
type ErrorKind int

const (
	// ErrInvalidParam 参数非法（如数量非正、冰山显示量大于总量）。
	ErrInvalidParam ErrorKind = iota
	// ErrDuplicateClientID 新委托的客户端编号与已接收委托重复。
	ErrDuplicateClientID
	// ErrOrderNotFound 改量或撤单所指向的委托不存在（未被接受过）。
	ErrOrderNotFound
	// ErrOrderCompleted 改量或撤单的委托已完全成交。
	ErrOrderCompleted
	// ErrOrderCancelled 改量或撤单的委托已撤销。
	ErrOrderCancelled
)

// EngineError 是撮合引擎返回的可分类业务错误。
type EngineError struct {
	Kind ErrorKind
	msg  string
}

func (e *EngineError) Error() string { return e.msg }

func errf(kind ErrorKind, format string, args ...any) error {
	return &EngineError{Kind: kind, msg: fmt.Sprintf(format, args...)}
}
