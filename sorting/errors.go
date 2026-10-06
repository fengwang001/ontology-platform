package sorting

import "fmt"

// ErrKind 错误类别，按判定优先级升序排列（数值越小优先级越高）：
// 参数非法 > 时钟回退 > 对象不存在 > 状态不符 > 网点不符 > 业务拒绝。
type ErrKind int

const (
	ErrInvalidParam   ErrKind = iota // 参数非法
	ErrClockRollback                 // 时钟回退
	ErrNotFound                      // 对象不存在（含“无开放集袋”）
	ErrStateMismatch                 // 状态不符
	ErrSiteMismatch                  // 网点不符
	ErrBusinessReject                // 业务拒绝（单件超重、运单号重复）
)

func (k ErrKind) String() string {
	switch k {
	case ErrInvalidParam:
		return "参数非法"
	case ErrClockRollback:
		return "时钟回退"
	case ErrNotFound:
		return "对象不存在"
	case ErrStateMismatch:
		return "状态不符"
	case ErrSiteMismatch:
		return "网点不符"
	case ErrBusinessReject:
		return "业务拒绝"
	}
	return "未知错误"
}

// Error 分拣系统错误，携带类别与具体原因。
type Error struct {
	Kind   ErrKind
	Reason string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Kind, e.Reason)
}

// KindOf 提取错误的类别；非本系统错误返回 ok=false。
func KindOf(err error) (ErrKind, bool) {
	if e, ok := err.(*Error); ok {
		return e.Kind, true
	}
	return 0, false
}

func errInvalidParam(format string, args ...any) *Error {
	return &Error{Kind: ErrInvalidParam, Reason: fmt.Sprintf(format, args...)}
}

func errClockRollback(now, last int64) *Error {
	return &Error{Kind: ErrClockRollback,
		Reason: fmt.Sprintf("操作时刻 %d 小于上次已接受时刻 %d", now, last)}
}

func errNotFound(format string, args ...any) *Error {
	return &Error{Kind: ErrNotFound, Reason: fmt.Sprintf(format, args...)}
}

func errStateMismatch(format string, args ...any) *Error {
	return &Error{Kind: ErrStateMismatch, Reason: fmt.Sprintf(format, args...)}
}

func errSiteMismatch(format string, args ...any) *Error {
	return &Error{Kind: ErrSiteMismatch, Reason: fmt.Sprintf(format, args...)}
}

func errBusinessReject(format string, args ...any) *Error {
	return &Error{Kind: ErrBusinessReject, Reason: fmt.Sprintf(format, args...)}
}
