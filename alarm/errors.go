package alarm

// ErrorCode 用于区分被拒绝操作的类别。
type ErrorCode int

const (
	ErrInvalidArg      ErrorCode = 1 // 参数非法（含原因/单号为空）
	ErrClockRollback   ErrorCode = 2 // 时钟回退
	ErrNoSuchPoint     ErrorCode = 3 // 报警点不存在
	ErrNoPermission    ErrorCode = 4 // 无权限
	ErrStateNotAllowed ErrorCode = 5 // 状态不允许（紧急不可屏蔽、已屏蔽、已停用等）
	ErrShelveTooLong   ErrorCode = 6 // 屏蔽时长超过上限
)

// AlarmError 携带可区分的错误码与说明。
type AlarmError struct {
	Code ErrorCode
	Msg  string
}

func (e *AlarmError) Error() string {
	return e.Code.String() + ": " + e.Msg
}

func (c ErrorCode) String() string {
	switch c {
	case ErrInvalidArg:
		return "invalid-argument"
	case ErrClockRollback:
		return "clock-rollback"
	case ErrNoSuchPoint:
		return "no-such-point"
	case ErrNoPermission:
		return "no-permission"
	case ErrStateNotAllowed:
		return "state-not-allowed"
	case ErrShelveTooLong:
		return "shelve-duration-too-long"
	default:
		return "unknown-error"
	}
}
