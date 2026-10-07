package chargeback

import "fmt"

// ErrCode 是可区分的错误类别。校验严格按优先级只报第一个。
type ErrCode int

const (
	ErrInvalidArgument     ErrCode = iota // 参数非法
	ErrClockRegression                    // 时钟回退
	ErrTransactionNotFound                // 交易不存在
	ErrWindowExpired                      // 提起窗口已过
	ErrDuplicateCase                      // 重复提起（同交易同原因已有商户胜案件）
	ErrExceedsDisputable                  // 超出可拒付余额
	ErrNoBasis                            // 重复扣款无依据交易
	ErrCaseNotFound                       // 案件不存在
	ErrInvalidState                       // 当前状态不允许
)

func (c ErrCode) String() string {
	switch c {
	case ErrInvalidArgument:
		return "InvalidArgument"
	case ErrClockRegression:
		return "ClockRegression"
	case ErrTransactionNotFound:
		return "TransactionNotFound"
	case ErrWindowExpired:
		return "WindowExpired"
	case ErrDuplicateCase:
		return "DuplicateCase"
	case ErrExceedsDisputable:
		return "ExceedsDisputable"
	case ErrNoBasis:
		return "NoBasis"
	case ErrCaseNotFound:
		return "CaseNotFound"
	case ErrInvalidState:
		return "InvalidState"
	}
	return "Unknown"
}

// Error 是系统返回的唯一错误类型，携带可区分的错误码。
type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Msg)
}

func newError(code ErrCode, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}
