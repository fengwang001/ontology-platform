package correlate

import (
	"sync"
	"testing"
	"time"

	"ontology/slot"
)

// fakeClock is a manually advanced clock; it never calls time.Now itself.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Unix(0, 0)} }

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newTestCorrelator(t *testing.T, capacity int) (*Correlator, *fakeClock) {
	t.Helper()
	clock := newFakeClock()
	return New(capacity, clock.now), clock
}

func mustRequest(t *testing.T, c *Correlator, timeout time.Duration) slot.ID {
	t.Helper()
	id, err := c.Request(timeout)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	return id
}
