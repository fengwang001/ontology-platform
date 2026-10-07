package approval

import "errors"

var (
	ErrInvalidArgument       = errors.New("参数非法")
	ErrClockMovedBack        = errors.New("时钟回退")
	ErrNotFound              = errors.New("许可或环节不存在")
	ErrInvalidState          = errors.New("状态不允许")
	ErrForbidden             = errors.New("无权限")
	ErrPrerequisiteNotPassed = errors.New("前置环节未通过")
	ErrCorrectionLimit       = errors.New("补正次数超限")
)

type ErrorCode string

const (
	CodeInvalidArgument       ErrorCode = "INVALID_ARGUMENT"
	CodeClockMovedBack        ErrorCode = "CLOCK_MOVED_BACK"
	CodeNotFound              ErrorCode = "NOT_FOUND"
	CodeInvalidState          ErrorCode = "INVALID_STATE"
	CodeForbidden             ErrorCode = "FORBIDDEN"
	CodePrerequisiteNotPassed ErrorCode = "PREREQUISITE_NOT_PASSED"
	CodeCorrectionLimit       ErrorCode = "CORRECTION_LIMIT"
)

func CodeOf(err error) ErrorCode {
	switch {
	case errors.Is(err, ErrInvalidArgument):
		return CodeInvalidArgument
	case errors.Is(err, ErrClockMovedBack):
		return CodeClockMovedBack
	case errors.Is(err, ErrNotFound):
		return CodeNotFound
	case errors.Is(err, ErrInvalidState):
		return CodeInvalidState
	case errors.Is(err, ErrForbidden):
		return CodeForbidden
	case errors.Is(err, ErrPrerequisiteNotPassed):
		return CodePrerequisiteNotPassed
	case errors.Is(err, ErrCorrectionLimit):
		return CodeCorrectionLimit
	default:
		return ""
	}
}
