// Package api is the public entry point of the sliding-window rate limiter.
// It depends only on lim.
package api

import (
	"errors"
	"fmt"

	"ontology/lim"
)

// ErrInvalidConfig is the third distinct sentinel error, covering limit <= 0
// or window <= 0 at construction time.
var ErrInvalidConfig = errors.New("api: limit and window must both be >= 1")

// Limiter admits at most limit requests in any sliding window of length
// window, using caller-supplied monotonic integer timestamps.
type Limiter struct {
	l             *lim.Limiter
	limit, window int64
}

// New constructs a Limiter or returns ErrInvalidConfig without side effects.
func New(limit, window int64) (*Limiter, error) {
	if limit < 1 || window < 1 {
		return nil, ErrInvalidConfig
	}
	return &Limiter{l: lim.New(limit, window), limit: limit, window: window}, nil
}

// Allow reports whether the request at t is admitted. Clock violations
// return a lim sentinel error and change no state.
func (x *Limiter) Allow(t int64) (bool, error) { return x.l.Allow(t) }

// Accepted reports the cumulative number of admitted requests.
func (x *Limiter) Accepted() int { return x.l.Accepted() }

// snapshot returns the retained accepted timestamps and last observed t.
func (x *Limiter) snapshot() ([]int64, int64) { return x.l.Snapshot() }

// SelfCheck replays a built-in request sequence and verifies all four
// invariants from NOTES.md: naive-reference equivalence (I1), expiry (I2),
// monotonicity (I3), and no-trace-on-failure for the three error kinds (I4).
func (x *Limiter) SelfCheck() error {
	r, err := New(3, 10)
	if err != nil {
		return err
	}
	seq := []int64{0, 2, 5, 7, 10, 11, 12, 20}
	want := []bool{true, true, true, false, false, true, false, true}
	var ref []int64 // naive reference: every accepted timestamp so far
	for i, t := range seq {
		got, gerr := r.Allow(t)
		if gerr != nil || got != want[i] {
			return fmt.Errorf("selfcheck step %d: got (%v,%v), want %v", i, got, gerr, want[i])
		}
		k := 0
		for _, ts := range ref { // I1: naive rescan; same-t peers count
			if ts >= t-10 && ts <= t {
				k++
			}
		}
		if (int64(k) < 3) != want[i] {
			return fmt.Errorf("selfcheck step %d: disagrees with naive reference", i)
		}
		if want[i] {
			ref = append(ref, t)
		}
		set, lastT := r.snapshot()
		var live []int64 // queue retains only window-live accepted entries
		for _, ts := range ref {
			if ts >= lastT-10 {
				live = append(live, ts)
			}
		}
		for _, ts := range set { // I2: nothing strictly older than lastT-window
			if ts < lastT-10 {
				return fmt.Errorf("selfcheck step %d: expired %d retained", i, ts)
			}
		}
		for j := 1; j < len(set); j++ { // I3: accepted set ascending/monotone
			if set[j] < set[j-1] {
				return fmt.Errorf("selfcheck step %d: non-monotone set %v", i, set)
			}
		}
		if fmt.Sprint(set) != fmt.Sprint(live) {
			return fmt.Errorf("selfcheck step %d: set %v != live reference %v", i, set, live)
		}
	}
	return checkFailuresLeaveNoTrace()
}

func checkFailuresLeaveNoTrace() error {
	if _, err := New(0, 10); !errors.Is(err, ErrInvalidConfig) {
		return fmt.Errorf("selfcheck: limit<=0 error = %v", err)
	}
	if _, err := New(3, -1); !errors.Is(err, ErrInvalidConfig) {
		return fmt.Errorf("selfcheck: window<=0 error = %v", err)
	}
	r, _ := New(3, 10)
	r.Allow(5)
	before, lastBefore := r.snapshot()
	if ok, err := r.Allow(-1); err != lim.ErrNegativeTime || ok {
		return fmt.Errorf("selfcheck: negative-t error = %v", err)
	}
	if ok, err := r.Allow(4); err != lim.ErrClockRollback || ok {
		return fmt.Errorf("selfcheck: rollback error = %v", err)
	}
	after, lastAfter := r.snapshot() // I4: set and lastT unchanged
	if fmt.Sprint(after) != fmt.Sprint(before) || lastAfter != lastBefore {
		return fmt.Errorf("selfcheck: state changed after rejection")
	}
	if ok, err := r.Allow(5); !ok || err != nil { // limiter still usable
		return fmt.Errorf("selfcheck: limiter unusable after rejections")
	}
	return nil
}
