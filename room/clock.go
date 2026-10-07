package room

import "fmt"

// clock 为单调逻辑时钟：只接受不小于上一次的 now。
type clock struct {
	now int64
}

// check 校验 now 的取值范围与单调性，不推进时钟。
func (c *clock) check(now int64) *Error {
	if now < 0 || now > MaxNow {
		return &Error{Code: ErrCodeInvalidParam, Msg: fmt.Sprintf("now %d out of [0,%d]", now, MaxNow)}
	}
	if now < c.now {
		return &Error{Code: ErrCodeClockRollback, Msg: fmt.Sprintf("now %d < last accepted %d", now, c.now)}
	}
	return nil
}

// advance 把时钟推进到 now（调用前须已通过 check）。
func (c *clock) advance(now int64) { c.now = now }
