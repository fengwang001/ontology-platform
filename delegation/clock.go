package delegation

import (
	"sync"
	"time"
)

// Clock 提供模块使用的逻辑“当前时刻”，便于测试注入手动时钟。
// 实现必须保证单调不回退；Service 内部也会对回退做钳制。
type Clock interface {
	Now() time.Time
}

// RealClock 使用系统时间。
type RealClock struct{}

// Now 返回当前系统时间。
func (RealClock) Now() time.Time { return time.Now() }

// ManualClock 是测试用的手动时钟，仅可向前推进。
type ManualClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewManualClock 以 t0 为初始时刻创建手动时钟。
func NewManualClock(t0 time.Time) *ManualClock {
	return &ManualClock{now: t0}
}

// Now 返回当前手动时刻。
func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Set 把时钟设置为 t（不允许回退，回退会被钳制为当前值）。
func (c *ManualClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if t.After(c.now) {
		c.now = t
	}
}

// Advance 把时钟向前推进 d。
func (c *ManualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
