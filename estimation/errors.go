package estimation

import "fmt"

// ErrCode 标识被拒绝操作的类别。常量的声明顺序即规范要求的报告
// 优先级：参数非法 > 时钟回退 > 调用者不在会话 > 权限不足 >
// 状态不允许 > 无人投票。ErrAlreadyExists 为 Join 专用，按
// “状态不允许”的位次参与检查。
type ErrCode int

const (
	ErrInvalidParam     ErrCode = iota + 1 // 参数非法
	ErrClockRegression                     // 时钟回退
	ErrNotInSession                        // 调用者不在会话
	ErrPermissionDenied                    // 权限不足
	ErrInvalidState                        // 状态不允许
	ErrNoVotes                             // 无人投票
	ErrAlreadyExists                       // 已在会话内（Join 专用）
)

func (c ErrCode) String() string {
	switch c {
	case ErrInvalidParam:
		return "invalid_param"
	case ErrClockRegression:
		return "clock_regression"
	case ErrNotInSession:
		return "not_in_session"
	case ErrPermissionDenied:
		return "permission_denied"
	case ErrInvalidState:
		return "invalid_state"
	case ErrNoVotes:
		return "no_votes"
	case ErrAlreadyExists:
		return "already_exists"
	}
	return fmt.Sprintf("err_code(%d)", int(c))
}

// Error 为服务返回的唯一错误类型，携带类别码与可读信息。
type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Msg) }

func newError(code ErrCode, msg string) *Error { return &Error{Code: code, Msg: msg} }
