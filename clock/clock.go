// Package clock 提供可注入的时钟抽象，便于测试确定性控制时间。
package clock

import (
	"sync"
	"time"
)

// Clock 是时间来源接口。
type Clock interface {
	Now() time.Time
}

// Real 使用系统时钟。
type Real struct{}

// Now 返回当前系统时间。
func (Real) Now() time.Time { return time.Now() }

// Fake 是手动推进的时钟，并发安全。
type Fake struct {
	mu sync.Mutex
	t  time.Time
}

// NewFake 返回起点为 t 的假时钟。
func NewFake(t time.Time) *Fake { return &Fake{t: t} }

// Now 返回假时钟当前时间。
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

// Advance 把假时钟推进 d（可为负，用于注入回拨）。
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}
