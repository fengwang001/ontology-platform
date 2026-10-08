package gateway

import "sync/atomic"

// logicalClock 网关级单调逻辑时钟：记录上一个被接受操作的时刻。
// 使用原子操作实现，不同车辆之间不会因其而阻塞。
type logicalClock struct {
	v atomic.Int64
}

func newLogicalClock() *logicalClock {
	c := &logicalClock{}
	c.v.Store(-1)
	return c
}

// now 返回当前时钟值（上一个被接受操作的时刻），尚无操作时返回 -1。
func (c *logicalClock) now() int64 { return c.v.Load() }

// tryAdvance 尝试将时钟推进到 t。t 小于当前值时返回 false（时刻回退），
// 否则将时钟置为 max(当前值, t) 并返回 true。可并发调用。
func (c *logicalClock) tryAdvance(t int64) bool {
	for {
		cur := c.v.Load()
		if t < cur {
			return false
		}
		if t == cur {
			return true
		}
		if c.v.CompareAndSwap(cur, t) {
			return true
		}
	}
}
