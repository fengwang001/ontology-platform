package enrollment

import "fmt"

type ErrCode int

const (
	ErrInvalidArgument ErrCode = iota
	ErrClockRegression
	ErrNotFound
	ErrTerminalState
	ErrStateNotAllowed
	ErrExistingApplication
	ErrDeadlinePassed
	ErrNoPermission
	ErrLimitExceeded
	ErrEffectiveBeforeLatest
	ErrQuotaInsufficient
)

type OpError struct {
	Code ErrCode
	Msg  string
}

func (e *OpError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code.String(), e.Msg)
}

func (c ErrCode) String() string {
	switch c {
	case ErrInvalidArgument:
		return "invalid_argument"
	case ErrClockRegression:
		return "clock_regression"
	case ErrNotFound:
		return "not_found"
	case ErrTerminalState:
		return "terminal_state"
	case ErrStateNotAllowed:
		return "state_not_allowed"
	case ErrExistingApplication:
		return "existing_application"
	case ErrDeadlinePassed:
		return "deadline_passed"
	case ErrNoPermission:
		return "no_permission"
	case ErrLimitExceeded:
		return "limit_exceeded"
	case ErrEffectiveBeforeLatest:
		return "effective_before_latest"
	case ErrQuotaInsufficient:
		return "quota_insufficient"
	default:
		return "unknown"
	}
}

func newErr(code ErrCode, format string, args ...any) error {
	return &OpError{Code: code, Msg: fmt.Sprintf(format, args...)}
}
