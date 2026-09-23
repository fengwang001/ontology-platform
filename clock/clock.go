// Package clock 提供可注入的整数刻时钟。
package clock

import "sync"

// Clock 是单调不减的整数刻时钟，零值不可用，请用 New 构造。
type Clock struct {
	mu sync.RWMutex
	t  int64
}

// New 从刻 start（须 >= 0）构造时钟。
func New(start int64) *Clock {
	if start < 0 {
		start = 0
	}
	return &Clock{t: start}
}

// Now 返回当前刻。
func (c *Clock) Now() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.t
}

// Advance 把时钟推进到 t；t 早于当前刻返回 false 且状态不变。
func (c *Clock) Advance(t int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t < c.t {
		return false
	}
	c.t = t
	return true
}
