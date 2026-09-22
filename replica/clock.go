package replica

import (
	"sync"
	"time"
)

// Clock 返回当前时刻。所有时间判定只走注入时钟，
// 实现不得读取真实时间。
type Clock func() time.Duration

// ManualClock 是可手动推进的注入时钟，并发安全。
type ManualClock struct {
	mu sync.Mutex
	t  time.Duration
}

// NewManualClock 创建从 0 时刻开始的手动时钟。
func NewManualClock() *ManualClock { return &ManualClock{} }

// Now 返回当前时刻。
func (c *ManualClock) Now() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// Clock 返回适配 Clock 类型的读取函数。
func (c *ManualClock) Clock() Clock { return c.Now }

// Advance 推进时钟。
func (c *ManualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t += d
}

// Set 直接设置当前时刻。
func (c *ManualClock) Set(t time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}
