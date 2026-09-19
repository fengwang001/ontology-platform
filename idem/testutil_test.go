package idem

import (
	"sync/atomic"
	"time"
)

// fakeClock is a controllable time source. Time is held as Unix-millisecond
// units so tests can advance deterministically.
type fakeClock struct {
	ms atomic.Int64
}

func newFakeClock(startMs int64) *fakeClock {
	c := &fakeClock{}
	c.ms.Store(startMs)
	return c
}

func (c *fakeClock) now() time.Time {
	return time.UnixMilli(c.ms.Load())
}

func (c *fakeClock) advance(d time.Duration) {
	c.ms.Add(d.Milliseconds())
}

// newExecutor builds an Executor whose ttl is expressed in milliseconds.
func newExecutor(c *fakeClock, ttlMs int64) *Executor {
	return New(c.now, time.Duration(ttlMs)*time.Millisecond)
}
