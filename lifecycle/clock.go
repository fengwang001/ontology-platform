package lifecycle

import (
	"sync"
	"time"
)

// Clock abstracts time so tests can drive and, in rare cases, roll it back.
type Clock interface {
	Now() time.Time
}

// FixedClock is a manually controlled Clock. Set may move time backwards.
type FixedClock struct {
	mu  sync.RWMutex
	now time.Time
}

func NewFixedClock(start time.Time) *FixedClock {
	return &FixedClock{now: start}
}

func (c *FixedClock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

// Set installs t as the current time. It intentionally accepts t earlier than
// the previous value: clocks may roll back in rare operational situations.
func (c *FixedClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

func (c *FixedClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
