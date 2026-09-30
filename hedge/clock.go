package hedge

import (
	"sync"
	"time"
)

// Clock 抽象时钟，生产环境用 RealClock，测试注入 ManualClock。
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) Timer
}

// Timer 可取消的定时器。
type Timer interface {
	Stop() bool
}

// RealClock 基于 time 包的真实时钟。
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

func (RealClock) AfterFunc(d time.Duration, f func()) Timer {
	return &realTimer{t: time.AfterFunc(d, f)}
}

type realTimer struct{ t *time.Timer }

func (r *realTimer) Stop() bool { return r.t.Stop() }

// ManualClock 手动时钟：Advance/AdvanceTo 同步按时间序触发到期定时器，
// 同一时刻按注册先后触发，保证相同脚本重放结果完全一致。
type ManualClock struct {
	mu     sync.Mutex
	now    time.Time
	seq    int64
	timers []*manualTimer
}

func NewManualClock(start time.Time) *ManualClock {
	return &ManualClock{now: start}
}

func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *ManualClock) Advance(d time.Duration) {
	c.AdvanceTo(c.Now().Add(d))
}

func (c *ManualClock) AdvanceTo(target time.Time) {
	for {
		c.mu.Lock()
		var next *manualTimer
		for _, t := range c.timers {
			if t.stopped || t.fired || t.at.After(target) {
				continue
			}
			if next == nil || t.at.Before(next.at) ||
				(t.at.Equal(next.at) && t.seq < next.seq) {
				next = t
			}
		}
		if next == nil {
			c.now = target
			c.mu.Unlock()
			return
		}
		next.fired = true
		c.now = next.at
		f := next.f
		c.mu.Unlock()
		f()
	}
}

func (c *ManualClock) AfterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	t := &manualTimer{clock: c, at: c.now.Add(d), seq: c.seq, f: f}
	c.timers = append(c.timers, t)
	return t
}

type manualTimer struct {
	clock   *ManualClock
	at      time.Time
	seq     int64
	f       func()
	stopped bool
	fired   bool
}

func (t *manualTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.fired || t.stopped {
		return false
	}
	t.stopped = true
	return true
}
