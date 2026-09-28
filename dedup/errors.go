package dedup

import "errors"

// 拒绝原因（RejectReason）取值：不同的非法情形可被区分。
const (
	ReasonInvalidParameter = "invalid_parameter" // 构造参数非法（TTL、MaxEntries）
	ReasonEmptyID          = "empty_id"          // 事件标识为空
	ReasonInvalidTime      = "invalid_time"      // 事件时间为零值
	ReasonMemoryLimit      = "memory_limit"      // 处理后记忆条数将超过上限
)

// 可 errors.Is 判定的哨兵错误。
var (
	// ErrInvalidParameter 表示配置参数非法。
	ErrInvalidParameter = errors.New("dedup: invalid parameter")
	// ErrEmptyID 表示事件标识为空。
	ErrEmptyID = errors.New("dedup: empty event id")
	// ErrInvalidTime 表示事件时间为零值。
	ErrInvalidTime = errors.New("dedup: invalid event time")
	// ErrMemoryLimit 表示接受事件后记忆条数将超过 MaxEntries。
	ErrMemoryLimit = errors.New("dedup: memory limit exceeded")
)

// RejectError 携带可区分的拒绝原因。
type RejectError struct {
	// Reason 为拒绝原因，取值为上方 Reason* 常量。
	Reason string
	// cause 为对应的哨兵错误，使 errors.Is 生效。
	cause error
}

func (e *RejectError) Error() string {
	return "dedup: rejected: " + e.Reason
}

// Unwrap 使 errors.Is(err, ErrEmptyID) 等判定成立。
func (e *RejectError) Unwrap() error {
	return e.cause
}

func newRejectError(reason string, cause error) *RejectError {
	return &RejectError{Reason: reason, cause: cause}
}
