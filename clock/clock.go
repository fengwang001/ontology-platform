// Package clock 提供可注入的时间源。
package clock

import "time"

// Clock 是分配器依赖的唯一时间来源，便于测试中确定性地推进与回拨。
type Clock interface {
	Now() time.Time
}

// RealClock 使用进程墙上时钟。
type RealClock struct{}

// Now 返回当前本地时间。
func (RealClock) Now() time.Time { return time.Now() }

// FakeClock 是手动控制的单调时钟，时间只在调用 Advance 时变化。
type FakeClock struct {
	t time.Time
}

// NewFakeClock 以起点 t 构造假时钟。
func NewFakeClock(t time.Time) *FakeClock {
	return &FakeClock{t: t}
}

// Now 返回当前假时间。
func (c *FakeClock) Now() time.Time { return c.t }

// Advance 向前推进 d；d 为负则制造时钟回拨。
func (c *FakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

// Set 直接设置时刻（可用于显式回拨）。
func (c *FakeClock) Set(t time.Time) { c.t = t }
