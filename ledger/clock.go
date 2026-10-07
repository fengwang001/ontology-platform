package ledger

// 合法时刻范围：0 到 10^9 的整数秒。
const (
	minNow = int64(0)
	maxNow = int64(1_000_000_000)
)

// clock 只随被接受的操作前进；被拒绝的操作不得改变时钟。
type clock struct {
	last    int64
	started bool
}

func (c *clock) check(now int64) *OpError {
	if c.started && now < c.last {
		return errf(ErrClockRollback, "now=%d 小于上一次被接受操作的 now=%d", now, c.last)
	}
	return nil
}

func (c *clock) advance(now int64) {
	c.last = now
	c.started = true
}

// now 返回当前时钟；尚未有任何被接受操作时返回 0。
func (c *clock) now() int64 {
	if !c.started {
		return 0
	}
	return c.last
}
