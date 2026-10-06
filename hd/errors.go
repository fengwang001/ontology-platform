package hd

// ErrorCode 为可区分的错误类别。优先级从低数字到高数字依次为：
// 参数非法 < 时钟回退 < 对象不存在 < 状态不符 < 感染隔离冲突 < 患者自身冲突 < 无可行机位。
// 当一次操作同时触发多个错误时，系统只报告优先级最靠前的那一个。
type ErrorCode int

const (
	ErrInvalidArgument ErrorCode = iota
	ErrClockRewind
	ErrNotFound
	ErrInvalidState
	ErrIsolationConflict
	ErrPatientConflict
	ErrNoFeasibleBay
)

func (c ErrorCode) String() string {
	switch c {
	case ErrInvalidArgument:
		return "INVALID_ARGUMENT"
	case ErrClockRewind:
		return "CLOCK_REWIND"
	case ErrNotFound:
		return "NOT_FOUND"
	case ErrInvalidState:
		return "INVALID_STATE"
	case ErrIsolationConflict:
		return "ISOLATION_CONFLICT"
	case ErrPatientConflict:
		return "PATIENT_CONFLICT"
	case ErrNoFeasibleBay:
		return "NO_FEASIBLE_BAY"
	default:
		return "UNKNOWN"
	}
}

// Error 携带稳定错误码与可读原因，便于测试精确断言与日志判定依据。
type Error struct {
	Code   ErrorCode
	Reason string
}

func (e *Error) Error() string {
	return e.Code.String() + ": " + e.Reason
}

func errf(code ErrorCode, format string, args ...any) error {
	return &Error{Code: code, Reason: sprintf(format, args...)}
}
