package compactlog

import (
	"sync"
	"time"
)

// Clock 提供日志写入时间，可注入以便确定性测试。
type Clock interface {
	Now() time.Time
}

// ManualClock 是手动推进的时钟，并发安全，用于确定性测试。
type ManualClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewManualClock 返回从 start 开始的手动时钟。
func NewManualClock(start time.Time) *ManualClock {
	return &ManualClock{now: start}
}

// Now 返回当前手动时间。
func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance 将时钟前进 d，不允许回退。
func (c *ManualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Set 强制设置时钟（测试用，可制造时间回退场景）。
func (c *ManualClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}
