package scheduler

import "errors"

// Code 错误类别，供调用方程序化区分。拒绝次序按常量声明顺序：
// 参数非法 < 时钟回退 < 对象不存在 < 状态类 < 提前量越界 < 时段已满。
type Code int

const (
	CodeInvalidParam Code = iota + 1
	CodeClockRollback
	CodeRegionNotFound
	CodeSlotNotFound
	CodeReservationNotFound
	CodeAlreadyCanceled
	CodeAlreadyDelivered
	CodeAlreadyReleased
	CodeNoChange
	CodeTooEarly
	CodeTooLate
	CodePastCutoff
	CodeNotReleased
	CodeSlotFull
)

// Error 系统返回的唯一错误类型。
type Error struct {
	Code Code
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func newError(code Code, msg string) *Error { return &Error{Code: code, Msg: msg} }

// CodeOf 提取错误类别；err 为 nil 时返回 0，非本系统错误返回 -1。
func CodeOf(err error) Code {
	if err == nil {
		return 0
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return -1
}
