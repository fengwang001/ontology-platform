// Package clock 提供可注入、可手动推进的纳秒级单调时钟。
package clock

import "sync"

// Clock 是一个停表式时钟:时间只随 Advance 改变。
type Clock struct {
	mu  sync.Mutex
	now int64 // UnixNano
}

// New 创建初始时刻为 unixNano 的时钟。
func New(unixNano int64) *Clock { return &Clock{now: unixNano} }

// Now 返回当前时刻(UnixNano)。
func (c *Clock) Now() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance 将时钟前进 d 纳秒,d 必须为非负。
func (c *Clock) Advance(d int64) {
	if d < 0 {
		panic("clock: negative advance")
	}
	c.mu.Lock()
	c.now += d
	c.mu.Unlock()
}

// AdvanceTo 把时钟设置到 t;早于当前时刻返回 false 且状态不变。
func (c *Clock) AdvanceTo(t int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t < c.now {
		return false
	}
	c.now = t
	return true
}
