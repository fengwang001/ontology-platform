package scheduler

// Clock 是由测试或调用方注入的单调时钟。
type Clock interface {
	Now() int64
	// Advance 把时钟推进到不早于当前时刻的 t；回拨返回 ErrClockRewind。
	Advance(t int64) (int64, error)
}

type injectedClock struct {
	now int64
}

func newInjectedClock(start int64) *injectedClock { return &injectedClock{now: start} }

func (c *injectedClock) Now() int64 { return c.now }

func (c *injectedClock) Advance(t int64) (int64, error) {
	if t < c.now {
		return c.now, ErrClockRewind
	}
	c.now = t
	return c.now, nil
}
