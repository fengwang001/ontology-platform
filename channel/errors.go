package channel

// Error 对服务可能报告的全部拒绝原因进行分类，错误类别即拒绝次序。
type Error string

const (
	ErrInvalidArg    Error = "invalid argument"              // 参数非法
	ErrClockRewind   Error = "clock rewind"                  // 时钟回退
	ErrNotMember     Error = "not a member"                  // 调用者不是成员
	ErrNotFound      Error = "message not found"             // 消息不存在
	ErrForbidden     Error = "permission denied"             // 权限不足
	ErrRecalled      Error = "message recalled"              // 状态：已撤回
	ErrTimeout       Error = "edit/recall window expired"    // 状态：超时
	ErrEditLimit     Error = "edit count limit reached"      // 状态：次数超限
	ErrWatermarkBack Error = "read watermark moved backward" // 状态：水位回退
	ErrOutOfRange    Error = "watermark beyond latest"       // 状态：越界
)

func (e Error) Error() string { return string(e) }
