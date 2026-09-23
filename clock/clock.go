// Package clock 提供可注入的整数刻时钟，不依赖其他包。
package clock

import "sync"

// Clock 是单调的整数刻时钟。刻是离散整数，无单位。
type Clock struct {
	mu  sync.Mutex
	now int64
}

// New 返回指向刻 0 的时钟。
func New() *Clock { return &Clock{} }

// Now 返回当前刻。
func (c *Clock) Now() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance 把时钟向前推进 d 刻，d 必须非负，返回推进后的刻。
func (c *Clock) Advance(d int64) int64 {
	if d < 0 {
		panic("clock: negative advance")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now += d
	return c.now
}

// SetTo 把时钟设置到不早于当前刻的 t，返回设置后的刻。
func (c *Clock) SetTo(t int64) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t > c.now {
		c.now = t
	}
	return c.now
}
