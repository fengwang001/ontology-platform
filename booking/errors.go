package booking

// ErrorCode 以程序化方式区分所有拒绝原因。
type ErrorCode int

const (
	// ErrInvalidParam 参数非法。
	ErrInvalidParam ErrorCode = iota
	// ErrClockRollback 操作时刻早于此前已接受操作的最大时刻。
	ErrClockRollback
	// ErrRegionNotFound 区域不存在。
	ErrRegionNotFound
	// ErrSlotNotFound 时段不存在。
	ErrSlotNotFound
	// ErrOrderNotFound 预约不存在。
	ErrOrderNotFound
	// ErrAlreadyCanceled 预约已取消。
	ErrAlreadyCanceled
	// ErrAlreadyDelivered 预约已送达。
	ErrAlreadyDelivered
	// ErrAlreadyReleased 预约已释放派单，不可改期。
	ErrAlreadyReleased
	// ErrNoRescheduleNeeded 改期目标时段与当前时段相同。
	ErrNoRescheduleNeeded
	// ErrNotReleased 送达要求预约先进入已释放状态。
	ErrNotReleased
	// ErrTooEarly 相对最早可预约提前量过早。
	ErrTooEarly
	// ErrTooLate 相对最晚可预约提前量过晚。
	ErrTooLate
	// ErrRescheduleDeadlinePassed 已过改期截止时刻。
	ErrRescheduleDeadlinePassed
	// ErrSlotFull 时段已满（或处于超额状态）。
	ErrSlotFull
)

// Error 携带可程序化判别的错误码。
type Error struct {
	Code ErrorCode
	msg  string
}

func (e *Error) Error() string { return e.msg }

func bookingError(code ErrorCode, msg string) *Error {
	return &Error{Code: code, msg: msg}
}

// CodeOf 提取错误码；nil 返回 -1。
func CodeOf(err error) ErrorCode {
	if err == nil {
		return -1
	}
	var be *Error
	if e, ok := err.(*Error); ok {
		be = e
	}
	return be.Code
}
