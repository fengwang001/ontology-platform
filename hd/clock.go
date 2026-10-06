package hd

// clock 记录最近一次被接受操作的 now，实现单调时钟约束。
// 被拒绝的操作不更新 last；所有校验入口先做参数检查再检查时钟。
type clock struct {
	last    int
	started bool
}

func (c *clock) check(now int) error {
	if c.started && now < c.last {
		return errf(ErrClockRewind, "now %d < last accepted %d", now, c.last)
	}
	return nil
}

func (c *clock) accept(now int) {
	c.last = now
	c.started = true
}
