// Package clock 提供可注入的时钟抽象，便于测试确定性推进与回拨。
package clock

import (
	"sync"
	"time"
)

// Clock 返回当前时刻。
type Clock interface {
	Now() time.Time
}

// Real 是真实系统时钟。
type Real struct{}

// Now 返回系统当前时刻。
func (Real) Now() time.Time { return time.Now() }

// Fake 是可手动推进的测试时钟，并发安全。
type Fake struct {
	mu  sync.Mutex
	now time.Time
}

// NewFake 返回初始时刻为 t 的假时钟。
func NewFake(t time.Time) *Fake { return &Fake{now: t} }

// Now 返回假时钟当前时刻。
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Advance 将假时钟推进 d（d 可为负，用于注入回拨）。
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

// Set 直接设定假时钟时刻。
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = t
}
