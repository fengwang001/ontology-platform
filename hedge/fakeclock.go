package hedge

import (
	"sync"
	"time"
)

// FakeClock 是测试用的手动时钟：时间只在 Advance 时前进，
// 到期定时器按 (触发时刻, 创建顺序) 依次触发，保证行为可重放。
type FakeClock struct {
	mu     sync.Mutex
	now    time.Time
	seq    int
	timers map[int]*fakeTimer
}

type fakeTimer struct {
	clock   *FakeClock
	at      time.Time
	seq     int
	ch      chan time.Time
	fired   bool
	stopped bool
}

// NewFakeClock 创建从 start 开始的手动时钟。
func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{now: start, timers: make(map[int]*fakeTimer)}
}

func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *FakeClock) NewTimer(d time.Duration) Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{
		clock: c,
		at:    c.now.Add(d),
		seq:   c.seq,
		ch:    make(chan time.Time, 1),
	}
	c.seq++
	c.timers[t.seq] = t
	return t
}

// Advance 将时钟前进 d，并按顺序触发所有到期定时器。
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	for {
		var next *fakeTimer
		for _, t := range c.timers {
			if t.fired || t.stopped || t.at.After(c.now) {
				continue
			}
			if next == nil || t.at.Before(next.at) ||
				(t.at.Equal(next.at) && t.seq < next.seq) {
				next = t
			}
		}
		if next == nil {
			return
		}
		next.fired = true
		next.ch <- next.at
	}
}

func (t *fakeTimer) C() <-chan time.Time { return t.ch }

func (t *fakeTimer) Stop() {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	t.stopped = true
}
