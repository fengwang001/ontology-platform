package hedge

import "time"

// Clock 抽象时钟，便于在测试中注入可控时钟。
type Clock interface {
	Now() time.Time
	NewTimer(d time.Duration) Timer
}

// Timer 是一次性定时器。
type Timer interface {
	C() <-chan time.Time
	Stop()
}

type realClock struct{}

type realTimer struct {
	t *time.Timer
}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) NewTimer(d time.Duration) Timer {
	return &realTimer{t: time.NewTimer(d)}
}

func (r *realTimer) C() <-chan time.Time { return r.t.C }

func (r *realTimer) Stop() { r.t.Stop() }

// RealClock 返回基于 time 包的真实时钟。
func RealClock() Clock { return realClock{} }
