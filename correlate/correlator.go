package correlate

import (
	"sync"
	"time"

	"ontology/reclaim"
	"ontology/slot"
)

// Counter holds the observable outcome counters.
type Counter struct {
	// Completed counts replies that finished an in-flight request.
	Completed uint64
	// TimedOut counts requests expired by the lazy sweep.
	TimedOut uint64
	// Canceled counts requests canceled by the caller.
	Canceled uint64
	// OrphanUnknown counts replies for IDs never allocated.
	OrphanUnknown uint64
	// OrphanIdle counts replies for an ID free after a finished generation.
	OrphanIdle uint64
	// OrphanStale counts replies for an older generation while a newer one
	// holds the ID (late reply that must not complete the newer request).
	OrphanStale uint64
}

// Correlator maps reusable IDs to in-flight requests. Create one with New.
type Correlator struct {
	mu    sync.Mutex
	now   func() time.Time
	pool  *reclaim.Pool
	slots []*slot.Slot

	c Counter
}

// New creates a correlator bounded by capacity. Time is read exclusively from
// the injected now function; the implementation never calls time.Now or sets
// up timers.
func New(capacity int, now func() time.Time) *Correlator {
	slots := make([]*slot.Slot, capacity)
	for i := range slots {
		slots[i] = slot.New()
	}
	return &Correlator{
		now:   now,
		pool:  reclaim.New(capacity),
		slots: slots,
	}
}

// Capacity reports the hard limit on concurrently in-flight requests.
func (c *Correlator) Capacity() int { return c.pool.Capacity() }

// InFlight reports the current number of in-flight requests after applying
// any lazy timeouts at the injected current time.
func (c *Correlator) InFlight() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sweep(c.now())
	return c.pool.Size()
}

// Issue starts an in-flight request with the given timeout budget. On success
// its Token must be echoed by the reply. When the capacity limit is reached
// Issue returns ErrCapacity without altering any state or counter.
func (c *Correlator) Issue(timeout time.Duration) (Token, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	c.sweep(now)
	id, err := c.pool.Acquire()
	if err != nil {
		return Token{}, ErrCapacity
	}
	epoch, ok := c.slots[id].Allocate(now.Add(timeout))
	if !ok {
		_ = c.pool.Release(id) // unreachable: pool and slots agree on holders
		return Token{}, ErrCapacity
	}
	return Token{ID: id, Epoch: epoch}, nil
}

// sweep expires every waiting slot whose deadline now has reached. Timeout is
// left-closed, right-open (see slot.Slot.Expired). Expired slots are released
// immediately so their IDs become reusable.
func (c *Correlator) sweep(now time.Time) {
	for id, s := range c.slots {
		if s.Expired(now) && s.Expire() {
			c.c.TimedOut++
			_ = c.pool.Release(id)
		}
	}
}
