package narledger

// clock 保存系统接受过的最大时间戳。被拒绝的操作不会推进它。
type clock struct {
	lastNow int64
}

// check 仅判定时钟是否回退，不改变状态。
func (c *clock) check(op string, now int64) *OpError {
	if now < c.lastNow {
		return opError(op, ErrClockRollback,
			"now=%d 小于已接受操作的时间 %d", now, c.lastNow)
	}
	return nil
}

// advance 只在操作被接受后调用。
func (c *clock) advance(now int64) {
	if now > c.lastNow {
		c.lastNow = now
	}
}
