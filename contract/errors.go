package contract

import "fmt"

// ErrorCode 按需求规定的优先级排序（数值越小优先级越高）。
type ErrorCode int

const (
	ErrInvalidParam   ErrorCode = iota + 1 // 参数非法
	ErrClockRollback                       // 时钟回退
	ErrNotFound                            // 合同或协议不存在
	ErrIllegalState                        // 状态不允许
	ErrAuthExpired                         // 授权失效
	ErrMissingCosign                       // 缺少会签
	ErrSigningExpired                      // 签署超期
)

func (c ErrorCode) String() string {
	switch c {
	case ErrInvalidParam:
		return "INVALID_PARAM"
	case ErrClockRollback:
		return "CLOCK_ROLLBACK"
	case ErrNotFound:
		return "NOT_FOUND"
	case ErrIllegalState:
		return "ILLEGAL_STATE"
	case ErrAuthExpired:
		return "AUTH_EXPIRED"
	case ErrMissingCosign:
		return "MISSING_COSIGN"
	case ErrSigningExpired:
		return "SIGNING_EXPIRED"
	default:
		return "UNKNOWN"
	}
}

// ServiceError 携带可区分的错误类别。
type ServiceError struct {
	Code    ErrorCode
	Message string
}

func (e *ServiceError) Error() string {
	return e.Code.String() + ": " + e.Message
}

func errf(code ErrorCode, format string, args ...any) error {
	return &ServiceError{Code: code, Message: fmt.Sprintf(format, args...)}
}
