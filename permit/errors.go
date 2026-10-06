package permit

import "fmt"

// ErrorCode 错误类别，按规定的优先级排列。
type ErrorCode int

const (
	ErrNone ErrorCode = iota
	// ErrInvalidParam 参数非法（优先级最高）。
	ErrInvalidParam
	// ErrClockRollback 时钟回退。
	ErrClockRollback
	// ErrNotFound 许可或环节不存在。
	ErrNotFound
	// ErrStateNotAllowed 状态不允许（含终局后操作）。
	ErrStateNotAllowed
	// ErrNoPermission 无权限。
	ErrNoPermission
	// ErrPrereqNotPassed 前置环节未通过。
	ErrPrereqNotPassed
	// ErrSupplementLimit 补正次数超限（优先级最低）。
	ErrSupplementLimit
)

// Error 携带可区分的错误类别。
type Error struct {
	Code ErrorCode
	Msg  string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", codeName[e.Code], e.Msg)
}

var codeName = map[ErrorCode]string{
	ErrInvalidParam:    "invalid_param",
	ErrClockRollback:   "clock_rollback",
	ErrNotFound:        "not_found",
	ErrStateNotAllowed: "state_not_allowed",
	ErrNoPermission:    "no_permission",
	ErrPrereqNotPassed: "prereq_not_passed",
	ErrSupplementLimit: "supplement_limit",
}

func errf(code ErrorCode, format string, args ...any) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// AsError 从 error 中取出 *Error。
func AsError(err error) (*Error, bool) {
	e, ok := err.(*Error)
	return e, ok
}
