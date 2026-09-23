package chunker

import (
	"sync"
	"time"
)

// ManualClock 是测试与演示用的可手动推进的注入时钟。
type ManualClock struct {
	mu sync.Mutex
	t  time.Time
}

// NewManualClock 从给定时刻创建手动时钟。
func NewManualClock(t time.Time) *ManualClock {
	return &ManualClock{t: t}
}

// Now 返回当前注入时刻。
func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

// Advance 把注入时钟向前推进 d。
func (c *ManualClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}
