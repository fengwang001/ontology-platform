package cpe

import "errors"

// ErrKind 区分错误类别。检查优先级（只报第一个）：
// 参数非法 > 时钟回退 > 持证人或记录不存在 > 状态不允许 > 重复登记 > 超出更正期限 > 证书已失效。
type ErrKind int

const (
	ErrNone ErrKind = iota
	ErrInvalidParam
	ErrClockRollback
	ErrNotFound
	ErrStateNotAllowed
	ErrDuplicate
	ErrCorrectionWindowExpired
	ErrCertificateExpired
)

func (k ErrKind) String() string {
	switch k {
	case ErrNone:
		return "none"
	case ErrInvalidParam:
		return "invalid_param"
	case ErrClockRollback:
		return "clock_rollback"
	case ErrNotFound:
		return "not_found"
	case ErrStateNotAllowed:
		return "state_not_allowed"
	case ErrDuplicate:
		return "duplicate"
	case ErrCorrectionWindowExpired:
		return "correction_window_expired"
	case ErrCertificateExpired:
		return "certificate_expired"
	default:
		return "unknown"
	}
}

// Error 是服务返回的业务错误，携带类别与说明。
type Error struct {
	Kind ErrKind
	Msg  string
}

func (e *Error) Error() string { return e.Kind.String() + ": " + e.Msg }

func newErr(kind ErrKind, msg string) *Error { return &Error{Kind: kind, Msg: msg} }

// ErrKindOf 提取错误的类别；err 为 nil 时返回 ErrNone。
func ErrKindOf(err error) ErrKind {
	if err == nil {
		return ErrNone
	}
	var ce *Error
	if errors.As(err, &ce) {
		return ce.Kind
	}
	return ErrInvalidParam
}
