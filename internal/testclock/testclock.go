// Package testclock 提供可手动推进的时钟，用于测试与演示中注入
// now func() time.Time，避免依赖真实时间。
package testclock

import (
	"sync"
	"time"
)

// Clock 是并发安全的手动时钟。
type Clock struct {
	mu sync.Mutex
	t  time.Time
}

// New 返回一个从 start 开始的手动时钟。
func New(start time.Time) *Clock {
	return &Clock{t: start}
}

// Now 返回当前时钟读数，可直接作为注入时钟使用。
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// Advance 把时钟向前推进 d。
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// Set 把时钟直接设为 t。
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}
