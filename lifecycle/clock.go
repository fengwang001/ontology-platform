package lifecycle

import "time"

// Clock 抽象系统时钟。服务内部永不回写时钟，时间只能由外部推进。
type Clock interface {
	Now() time.Time
}

// SystemClock 使用墙钟时间。
type SystemClock struct{}

// FakeClock 是可由调用方显式推进的测试时钟，服务内部不会修改它。
type FakeClock struct {
	t time.Time
}

func (SystemClock) Now() time.Time { return time.Now() }

func NewFakeClock(t time.Time) *FakeClock { return &FakeClock{t: t} }
func (c *FakeClock) Now() time.Time       { return c.t }
func (c *FakeClock) Advance(d time.Duration) {
	c.t = c.t.Add(d)
}
func (c *FakeClock) Set(t time.Time) { c.t = t }
