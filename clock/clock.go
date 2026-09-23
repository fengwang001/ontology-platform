// Package clock 提供可注入的整数刻时钟。
package clock

// Clock 是单调递增的整数刻时钟。时间只允许通过 Advance/Tick 前进。
type Clock struct {
	now int64
}

// New 返回指向刻 0 的时钟。
func New() *Clock { return &Clock{} }

// Now 返回当前刻。
func (c *Clock) Now() int64 { return c.now }

// Advance 把时钟向前推进 d 刻；d 必须非负。
func (c *Clock) Advance(d int64) {
	if d < 0 {
		panic("clock: negative advance")
	}
	c.now += d
}

// Tick 推进一刻并返回新的当前刻。
func (c *Clock) Tick() int64 {
	c.now++
	return c.now
}
