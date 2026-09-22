package lease

import "sync/atomic"

// fakeClock 是测试用的逻辑时钟，单位毫秒。
// 实现不调用 time.Now()，时间只来自这里，与实现的注入约定一致。
type fakeClock struct {
	t atomic.Int64
}

func newFakeClock(start int64) *fakeClock {
	c := &fakeClock{}
	c.t.Store(start)
	return c
}

func (c *fakeClock) now() int64 { return c.t.Load() }

// set 直接把时钟拨到指定时刻，用于确定性地制造"恰好到期"等边界。
func (c *fakeClock) set(v int64) { c.t.Store(v) }

// advance 把时钟向前推 d 毫秒。
func (c *fakeClock) advance(d int64) { c.t.Add(d) }
