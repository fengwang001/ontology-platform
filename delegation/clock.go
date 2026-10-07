package delegation

import "time"

// Clock 抽象时间来源，便于测试注入可控时钟。
type Clock interface {
	Now() time.Time
}

// SystemClock 使用真实系统时间。
type SystemClock struct{}

// Now 返回当前系统时间。
func (SystemClock) Now() time.Time { return time.Now() }

// ManualClock 是测试用的手动时钟，并发安全由 Service 的互斥锁保证；
// 单独使用时 Advance 与 Now 不构成并发场景。
type ManualClock struct {
	t time.Time
}

// NewManualClock 创建从 start 开始的手动时钟。
func NewManualClock(start time.Time) *ManualClock { return &ManualClock{t: start} }

// Now 返回手动时钟当前时刻。
func (c *ManualClock) Now() time.Time { return c.t }

// Advance 将手动时钟推进 d。
func (c *ManualClock) Advance(d time.Duration) { c.t = c.t.Add(d) }
