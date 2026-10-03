package caps

// Reason 是拒绝原因的稳定枚举，便于精确复现与断言。
type Reason uint8

const (
	ReasonOK Reason = iota
	ReasonInvalid
	ReasonForbidden
	ReasonNoSession
	ReasonNoVersion
	ReasonMissing
	ReasonDenied
	ReasonWindow
	ReasonNotEnabled
)

// Error 携带拒绝原因与最小报错特性编号（无则为 -1）。
type Error struct {
	Reason  Reason
	Feature int
	Detail  string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	s := reasonText(e.Reason)
	if e.Feature >= 0 {
		s += " feature=" + itoa(e.Feature)
	}
	if e.Detail != "" {
		s += ": " + e.Detail
	}
	return s
}

func reasonText(r Reason) string {
	switch r {
	case ReasonInvalid:
		return "ErrInvalid"
	case ReasonForbidden:
		return "ErrForbidden"
	case ReasonNoSession:
		return "ErrNoSession"
	case ReasonNoVersion:
		return "ErrNoVersion"
	case ReasonMissing:
		return "ErrMissing"
	case ReasonDenied:
		return "ErrDenied"
	case ReasonWindow:
		return "ErrWindow"
	case ReasonNotEnabled:
		return "ErrNotEnabled"
	default:
		return "ErrOK"
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// AsError 从任意错误中取出 *Error，失败返回 nil。
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	if e, ok := err.(*Error); ok {
		return e
	}
	return nil
}

// NewError 构造一个指定原因的错误。
func NewError(r Reason, feature int, detail string) *Error {
	return &Error{Reason: r, Feature: feature, Detail: detail}
}
