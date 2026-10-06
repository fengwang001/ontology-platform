package contacttracing

import "fmt"

// ErrKind 是系统内可区分的错误类别，按优先级只报第一个。
type ErrKind int

const (
	ErrInvalidParameter ErrKind = iota + 1 // 参数非法
	ErrClockRollback                       // 时钟回退
	ErrNotFound                            // 对象不存在
	ErrInvalidState                        // 状态不符
	ErrStayConflict                        // 住宿冲突
)

func (k ErrKind) String() string {
	switch k {
	case ErrInvalidParameter:
		return "INVALID_PARAMETER"
	case ErrClockRollback:
		return "CLOCK_ROLLBACK"
	case ErrNotFound:
		return "NOT_FOUND"
	case ErrInvalidState:
		return "INVALID_STATE"
	case ErrStayConflict:
		return "STAY_CONFLICT"
	default:
		return "UNKNOWN"
	}
}

// Error 是携带错误类别的业务错误。
type Error struct {
	Kind ErrKind
	Msg  string
}

func (e *Error) Error() string { return e.Kind.String() + ": " + e.Msg }

func errf(kind ErrKind, format string, args ...any) error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}
