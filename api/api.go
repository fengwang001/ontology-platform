// Package api is the public face of the sliding-window exact distinct
// counter: New / Feed / Distinct / SelfCheck. It depends only on wdist.
package api

import (
	"errors"
	"sync"

	"ontology/wdist"
)

// Event is a change {Key, TS}; timestamps arrive non-decreasing.
type Event = wdist.Event

// Four distinguishable sentinel errors; callers use errors.Is.
var (
	// ErrInvalidWindow: W < 1.
	ErrInvalidWindow = errors.New("api: window size W must be >= 1")
	// ErrInvalidBucket: b < 1 or b > W.
	ErrInvalidBucket = errors.New("api: bucket size b must satisfy 1 <= b <= W")
	// ErrInvalidEvent: negative Key or negative TS.
	ErrInvalidEvent = errors.New("api: key and timestamp must be non-negative")
	// ErrTSRollback: a TS smaller than an already accepted TS.
	ErrTSRollback = errors.New("api: timestamps must be non-decreasing")
)

// Counter is safe for concurrent use; all state lives in process memory.
type Counter struct {
	mu sync.RWMutex
	c  *wdist.Counter
	w  int64
}

// New validates W and b; it returns a distinguishable error and no
// counter when the parameters are illegal.
func New(W, b int64) (*Counter, error) {
	if W < 1 {
		return nil, ErrInvalidWindow
	}
	if b < 1 || b > W {
		return nil, ErrInvalidBucket
	}
	return &Counter{c: wdist.New(W, b), w: W}, nil
}

// Feed validates the whole batch first (including rollback against the
// last accepted TS); any rejection leaves every state untouched. On
// success the whole batch is applied and expired once at its final T.
func (c *Counter) Feed(evs []Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch err := c.c.Check(evs); {
	case errors.Is(err, wdist.ErrInvalidEvent):
		return ErrInvalidEvent
	case errors.Is(err, wdist.ErrTSRollback):
		return ErrTSRollback
	case err != nil:
		return err
	}
	c.c.Apply(evs)
	return nil
}

// Distinct returns the number of distinct keys with last[key] in
// (T-W, T], where T is the latest accepted timestamp.
func (c *Counter) Distinct() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.c.Distinct()
}

// SelfCheck runs built-in sequences and verifies all four invariants:
// per-step equality with a naive rescan, boundary semantics, last
// monotonicity (violations would break naive equality), and that each
// of the four rejection kinds is distinguishable and leaves no trace.
func (c *Counter) SelfCheck() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	W, b := int64(10), int64(5)
	steps := []Event{{Key: 1, TS: 1}, {Key: 2, TS: 3}, {Key: 5, TS: 4}, {Key: 1, TS: 6}, {Key: 3, TS: 11}, {Key: 4, TS: 13}}
	want := []int{1, 2, 3, 3, 4, 4}
	cc, err := New(W, b)
	if err != nil {
		return false
	}
	var all []Event
	for i, e := range steps {
		if err := cc.Feed([]Event{e}); err != nil || cc.Distinct() != want[i] {
			return false
		}
		all = append(all, e)
		if cc.Distinct() != naive(all, W) { // invariant 1 (+2, +3)
			return false
		}
	}
	if cc.Distinct() != 4 { // key2@3 == T-W excluded; key4@13 == T included
		return false
	}
	return checkRejections(cc) // invariant 4: distinguishable, no trace
}

// checkRejections verifies the four error kinds and no-trace semantics.
func checkRejections(cc *Counter) bool {
	errs := []error{ErrInvalidWindow, ErrInvalidBucket, ErrInvalidEvent, ErrTSRollback}
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			if errs[i] == errs[j] {
				return false // the four must be mutually distinguishable
			}
		}
	}
	if _, err := New(0, 1); !errors.Is(err, ErrInvalidWindow) {
		return false
	}
	if _, err := New(10, 11); !errors.Is(err, ErrInvalidBucket) {
		return false
	}
	before := cc.Distinct()
	badBatches := [][]Event{
		{{Key: 2, TS: 14}, {Key: -1, TS: 13}},
		{{Key: 2, TS: -1}},
		{{Key: 2, TS: 11}},
		{{Key: 2, TS: 13}, {Key: 2, TS: 12}},
	}
	wants := []error{ErrInvalidEvent, ErrInvalidEvent, ErrTSRollback, ErrTSRollback}
	for i, bad := range badBatches {
		if !errors.Is(cc.Feed(bad), wants[i]) || cc.Distinct() != before {
			return false // rejected batch must change nothing
		}
	}
	return cc.Feed([]Event{{Key: 6, TS: 13}}) == nil && cc.Distinct() == before+1 // still usable; equal TS keeps T=13
}

// naive rescans every accepted event and counts distinct keys whose
// event timestamp lies in (T-W, T]; equivalent to the last[] rule.
func naive(all []Event, W int64) int {
	if len(all) == 0 {
		return 0
	}
	T := all[len(all)-1].TS
	seen := map[int]bool{}
	for _, e := range all {
		if e.TS > T-W && e.TS <= T {
			seen[e.Key] = true
		}
	}
	return len(seen)
}
