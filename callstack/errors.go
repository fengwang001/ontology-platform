package callstack

import (
	"fmt"
	"strings"
)

// ErrorKind 是运行时可被程序区分的错误类别。
// 拒绝调用时的优先级在任何组合下都固定为：
// 已关闭 > 未定义函数 > 参数不匹配 > 深度超限 > 配额超限。
type ErrorKind int

const (
	// ErrUndefined：引用了不存在的函数。
	ErrUndefined ErrorKind = iota + 1
	// ErrArity：实参个数与函数声明不符。
	ErrArity
	// ErrDepth：帧数达到上限后的下一次非尾调用。
	ErrDepth
	// ErrQuota：新帧槽位数超出帧内存配额。
	ErrQuota
	// ErrUnhandled：异常未被任何保护区域处理。
	ErrUnhandled
	// ErrClosed：实例已关闭。
	ErrClosed
)

// String 返回错误类别的稳定英文标识，便于断言与日志。
func (k ErrorKind) String() string {
	switch k {
	case ErrUndefined:
		return "undefined-function"
	case ErrArity:
		return "arity-mismatch"
	case ErrDepth:
		return "depth-limit-exceeded"
	case ErrQuota:
		return "quota-limit-exceeded"
	case ErrUnhandled:
		return "unhandled-exception"
	case ErrClosed:
		return "instance-closed"
	default:
		return "unknown"
	}
}

// Error 是本子系统产生的全部错误。
// 程序可用类型断言配合 Kind 区分错误类别。
// ErrUnhandled 另外携带异常值 Value 与传播途经帧 Backtrace。
type Error struct {
	Kind      ErrorKind
	Message   string
	Value     int64
	Backtrace []BacktraceFrame
}

func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s", e.Kind, e.Message)
	if e.Kind == ErrUnhandled {
		fmt.Fprintf(&b, " (value=%d)", e.Value)
		if len(e.Backtrace) > 0 {
			b.WriteString("; propagated through: ")
			for i, f := range e.Backtrace {
				if i > 0 {
					b.WriteString(" <- ")
				}
				fmt.Fprintf(&b, "%s(+%d folded)", f.Func, f.FoldedBefore)
			}
		}
	}
	return b.String()
}

func newError(kind ErrorKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

// FormatBacktrace 把回溯渲染成诊断文本，顺序为最内层帧在前。
// 每行一个帧，含「此位置之前被尾调用省略的帧数」。
func FormatBacktrace(bt []BacktraceFrame) string {
	var b strings.Builder
	for i, f := range bt {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "#%d %s slots=%d folded-before=%d", i, f.Func, f.Slots, f.FoldedBefore)
	}
	return b.String()
}
