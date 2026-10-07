package core

// ErrorCode 是写入拒绝原因的可区分编码。
type ErrorCode string

const (
	// ErrInvalidArgument 参数非法：主键为空、业务时间起点非法、并发凭证格式非法。
	ErrInvalidArgument ErrorCode = "INVALID_ARGUMENT"
	// ErrConcurrencyConflict 乐观并发凭证与当前最新版本不一致。
	ErrConcurrencyConflict ErrorCode = "CONCURRENCY_CONFLICT"
	// ErrBeforeBoundary 业务时间起点早于该主键已确认提交的最早可追溯边界。
	ErrBeforeBoundary ErrorCode = "BEFORE_TRACEABLE_BOUNDARY"
)

// Error 是归一化后的写入错误，三类拒绝可相互区分。
type Error struct {
	Code    ErrorCode
	Message string
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }
