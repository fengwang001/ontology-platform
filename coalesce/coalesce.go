// Package coalesce implements a NIC-style interrupt throttling coalescer
// with an injected clock.
package coalesce

import (
	"errors"
	"fmt"
	"sync"
)

// Record is a single emitted interrupt: the time it fired and the number
// of coalesced events it carried.
type Record struct {
	Time  int64
	Count int64
}

// Distinguishable rejection reasons.
var (
	ErrNegativeGap    = errors.New("coalesce: gap must be >= 0")
	ErrBadThreshold   = errors.New("coalesce: threshold must be >= 1")
	ErrTimeRegression = errors.New("coalesce: operation time before last successful operation time")
	ErrBadEventCount  = errors.New("coalesce: event count must be >= 1")
	ErrNotWaitingAck  = errors.New("coalesce: ack while not waiting for acknowledgment")
)

// Coalescer merges events into throttled interrupts. Safe for concurrent use.
type Coalescer struct {
	mu      sync.Mutex
	gap     int64
	thr     int64
	p       int64
	last    int64
	fired   bool
	waitAck bool
	masked  bool
	records []Record
	lastT   int64
	hasT    bool
	events  int64
	acks    int64
}

// New builds a Coalescer with minimum inter-interrupt gap and event-count
// threshold thr.
func New(gap, thr int64) (*Coalescer, error) {
	if gap < 0 {
		return nil, fmt.Errorf("gap=%d: %w", gap, ErrNegativeGap)
	}
	if thr < 1 {
		return nil, fmt.Errorf("thr=%d: %w", thr, ErrBadThreshold)
	}
	return &Coalescer{gap: gap, thr: thr}, nil
}

// Event adds n pending events at time t.
func (c *Coalescer) Event(t, n int64) error {
	return c.op(t, func() error {
		if n < 1 {
			return fmt.Errorf("n=%d: %w", n, ErrBadEventCount)
		}
		return nil
	}, func() {
		c.p += n
		c.events += n
	})
}

// Tick performs only the catch-up and fire checks at time t.
func (c *Coalescer) Tick(t int64) error {
	return c.op(t, nil, nil)
}

// Ack acknowledges the last interrupt at time t.
func (c *Coalescer) Ack(t int64) error {
	return c.op(t, func() error {
		if !c.waitAck {
			return ErrNotWaitingAck
		}
		return nil
	}, func() {
		c.waitAck = false
		c.acks++
	})
}

// Mask masks interrupts at time t.
func (c *Coalescer) Mask(t int64) error {
	return c.op(t, nil, func() { c.masked = true })
}

// Unmask unmasks interrupts at time t.
func (c *Coalescer) Unmask(t int64) error {
	return c.op(t, nil, func() { c.masked = false })
}

// Records returns a copy of all emitted interrupt records in order.
func (c *Coalescer) Records() []Record {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.records) == 0 {
		return nil
	}
	out := make([]Record, len(c.records))
	copy(out, c.records)
	return out
}

// Snapshot is a consistent read-only view of the coalescer state.
type Snapshot struct {
	Pending    int64
	LastFire   int64
	Fired      bool
	WaitAck    bool
	Masked     bool
	Fires      int64
	Acks       int64
	EventTotal int64
}

// Snapshot returns the current state under the lock.
func (c *Coalescer) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Snapshot{
		Pending:    c.p,
		LastFire:   c.last,
		Fired:      c.fired,
		WaitAck:    c.waitAck,
		Masked:     c.masked,
		Fires:      int64(len(c.records)),
		Acks:       c.acks,
		EventTotal: c.events,
	}
}

// op runs one operation under the lock: validate (time regression first),
// then the three steps: catch-up fire, apply, fire check at t.
func (c *Coalescer) op(t int64, validate func() error, apply func()) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hasT && t < c.lastT {
		return fmt.Errorf("t=%d before last successful t=%d: %w", t, c.lastT, ErrTimeRegression)
	}
	if validate != nil {
		if err := validate(); err != nil {
			return err
		}
	}
	c.catchUp(t)
	if apply != nil {
		apply()
	}
	c.tryFire(t)
	c.lastT = t
	c.hasT = true
	return nil
}

// allow is the earliest fire time imposed by the gap; only meaningful once
// at least one interrupt has fired.
func (c *Coalescer) allow() int64 {
	return c.last + c.gap
}

// catchUp fires at the allow time (not t) when the gap has elapsed while
// fewer than thr events are pending.
func (c *Coalescer) catchUp(t int64) {
	if c.fired && c.p > 0 && !c.waitAck && !c.masked && c.p < c.thr && c.allow() <= t {
		c.fire(c.allow())
	}
}

// tryFire fires at t if the coalescer is currently fireable.
func (c *Coalescer) tryFire(t int64) {
	if c.p > 0 && !c.waitAck && !c.masked && (!c.fired || t >= c.allow() || c.p >= c.thr) {
		c.fire(t)
	}
}

// fire emits one interrupt carrying all pending events.
func (c *Coalescer) fire(at int64) {
	c.records = append(c.records, Record{Time: at, Count: c.p})
	c.p = 0
	c.last = at
	c.fired = true
	c.waitAck = true
}
