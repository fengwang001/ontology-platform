package lab

// Clock 是全局单调时钟：只接受不小于上一次被接受操作 now 的时刻。
type Clock struct {
	last    int64
	started bool
}

// Check 校验 now 的取值范围与单调性，不推进时钟。
func (c *Clock) Check(now int64) *Error {
	if now < 0 || now > MaxNow {
		return newError(ErrInvalidParam, "now=%d 超出 [0,%d]", now, MaxNow)
	}
	if c.started && now < c.last {
		return newError(ErrClockRollback, "now=%d 小于上次被接受操作的 now=%d", now, c.last)
	}
	return nil
}

// Advance 在操作被接受后推进时钟。
func (c *Clock) Advance(now int64) {
	c.last = now
	c.started = true
}
