package ontology

import "time"

// Clock 是租约判定的唯一时间权威，可注入假时钟用于测试。
type Clock interface {
	Now() time.Time
}

// RealClock 使用系统单调时钟。
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

// FakeClock 是测试用的手动推进时钟（测试内单线程推进）。
type FakeClock struct {
	now time.Time
}

func NewFakeClock(start time.Time) *FakeClock { return &FakeClock{now: start} }

func (c *FakeClock) Now() time.Time { return c.now }

func (c *FakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }
