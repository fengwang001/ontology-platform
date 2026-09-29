package isr

// Error 区分导致一次调用被整体拒绝的原因。
type Error struct {
	Reason string
}

func (e *Error) Error() string { return e.Reason }

const (
	reasonInvalidArgument = "isr: invalid argument"
	reasonUnknownReplica  = "isr: unknown replica"
	reasonInvalidOffset   = "isr: invalid offset"
	reasonClockWentBack   = "isr: clock went backwards"
)

var (
	// ErrInvalidArgument 参数非法（nil、空标识、非正时间增量语义等）。
	ErrInvalidArgument = &Error{Reason: reasonInvalidArgument}
	// ErrUnknownReplica 操作目标副本未注册。
	ErrUnknownReplica = &Error{Reason: reasonUnknownReplica}
	// ErrInvalidOffset 位点非法（小于已进度或越界）。
	ErrInvalidOffset = &Error{Reason: reasonInvalidOffset}
	// ErrClockWentBack 提供的时间早于本副本集已观察到的最大时间。
	ErrClockWentBack = &Error{Reason: reasonClockWentBack}
)
