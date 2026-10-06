package callstack

import "fmt"

// ErrorKind 为可被程序区分的错误类别。
type ErrorKind int

const (
	// ErrUndefinedFunction：引用不存在的函数。优先级最高（除实例已关闭外）。
	ErrUndefinedFunction ErrorKind = iota + 1
	// ErrArity：实参个数与形参个数不匹配。优先于深度与配额错误。
	ErrArity
	// ErrDepth：帧深到达上限后的下一次非尾调用。优先于配额错误。
	ErrDepth
	// ErrQuota：新帧的槽位放不下当前帧内存配额（复用事务失败时原帧完好）。
	ErrQuota
	// ErrUnhandled：异常穿过全部帧仍未被任何保护区域处理。
	ErrUnhandled
	// ErrClosed：解释器实例已关闭，不能再执行调用。
	ErrClosed
)

func (k ErrorKind) String() string {
	switch k {
	case ErrUndefinedFunction:
		return "undefined function"
	case ErrArity:
		return "argument count mismatch"
	case ErrDepth:
		return "stack depth limit exceeded"
	case ErrQuota:
		return "frame memory quota exceeded"
	case ErrUnhandled:
		return "unhandled exception"
	case ErrClosed:
		return "interpreter instance closed"
	default:
		return "unknown error"
	}
}

// FrameInfo 是回溯中的一帧：被调函数名，以及此帧之前被连续尾调用
// 省略（折叠）的帧数。
type FrameInfo struct {
	Function string
	Folded   int
}

// Error 为运行时错误。Kind 可用于程序区分；Unhandled 错误额外携带
// 异常负载与完整传播路径（含每帧的折叠计数）。
type Error struct {
	Kind    ErrorKind
	Message string
	Payload int64
	Trace   []FrameInfo
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	return e.Kind.String() + ": " + e.Message
}

// AsError 把任意 error 还原为 *Error；不匹配时返回 nil。
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	if e, ok := err.(*Error); ok {
		return e
	}
	return nil
}

func newErrorf(kind ErrorKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Message: fmt.Sprintf(format, args...)}
}
