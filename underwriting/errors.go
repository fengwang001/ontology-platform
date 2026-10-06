package underwriting

import "fmt"

// ErrKind 错误类别，调用方可据此以可区分的方式判别各类拒绝。
type ErrKind int

const (
	ErrInvalidParam   ErrKind = iota + 1 // 参数非法
	ErrAppNotFound                       // 投保单不存在
	ErrTerminal                          // 已终态
	ErrDeferred                          // 延期中
	ErrExamRegistered                    // 已登记体检
	ErrStateConflict                     // 当前状态不允许该操作（其余业务错误）
	ErrAppDuplicate                      // 投保单重复（其余业务错误）
	ErrRuleDuplicate                     // 规则重复
	ErrRuleNotFound                      // 规则不存在
)

func (k ErrKind) String() string {
	switch k {
	case ErrInvalidParam:
		return "参数非法"
	case ErrAppNotFound:
		return "投保单不存在"
	case ErrTerminal:
		return "已终态"
	case ErrDeferred:
		return "延期中"
	case ErrExamRegistered:
		return "已登记体检"
	case ErrStateConflict:
		return "状态不允许"
	case ErrAppDuplicate:
		return "投保单重复"
	case ErrRuleDuplicate:
		return "规则重复"
	case ErrRuleNotFound:
		return "规则不存在"
	}
	return "未知错误"
}

// Error 引擎返回的唯一错误类型，Kind 字段供调用方判别。
type Error struct {
	Kind ErrKind
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Kind, e.Msg)
}

// KindOf 取出错误的类别；非引擎错误返回 0。
func KindOf(err error) ErrKind {
	if e, ok := err.(*Error); ok {
		return e.Kind
	}
	return 0
}

func errf(kind ErrKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}
