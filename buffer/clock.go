package buffer

import (
	"sync"
	"time"
)

// ManualClock 是手动推进的时钟，用于确定性测试延迟触发。
// 它也可用于任何需要可控时钟的调用方。
type ManualClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewManualClock 返回从 start 开始的手动时钟。
func NewManualClock(start time.Time) *ManualClock {
	return &ManualClock{now: start}
}

// Now 实现 Clock。
func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance 把时钟向前推进 d。
func (c *ManualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
