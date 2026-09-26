// Package api is the public entry point of the sliding-window rate limiter.
package api

import (
	"errors"
	"reflect"

	"ontology/lim"
)

// ErrInvalidConfig is returned by New when limit or window is below 1.
var ErrInvalidConfig = errors.New("api: limit and window must both be >= 1")

// Limiter wraps the concurrency-safe lim.Limiter.
type Limiter struct {
	l *lim.Limiter
}

// New builds a limiter allowing at most limit requests in any sliding
// [t-window, t] window. Invalid configuration creates no state.
func New(limit, window int64) (*Limiter, error) {
	if limit < 1 || window < 1 {
		return nil, ErrInvalidConfig
	}
	return &Limiter{l: lim.New(limit, window)}, nil
}

// Allow reports whether the request at monotonic timestamp t is admitted.
func (x *Limiter) Allow(t int64) (bool, error) { return x.l.Allow(t) }

// Accepted reports the total number of admitted requests.
func (x *Limiter) Accepted() int { return int(x.l.Accepted()) }

// Snapshot reports retained accepted timestamps; it backs the demo only.
func (x *Limiter) Snapshot() []int64 { return x.l.Snapshot() }

// SelfCheck replays the built-in trace on a fresh limit=3, window=10 limiter
// and verifies the four invariants plus the three distinct sentinel errors.
// The receiver is never dereferenced or mutated.
func (x *Limiter) SelfCheck() bool {
	c, err := New(3, 10)
	if err != nil {
		return false
	}
	steps := []int64{0, 2, 5, 7, 10, 11, 12, 20}
	var hist []int64
	var prev int64
	ok := true
	for _, t := range steps {
		got, err := c.Allow(t)
		if err != nil {
			return false
		}
		k := 0
		for _, ts := range hist { // naive reference: scan every accepted ts
			if ts >= t-10 && ts <= t { // [t-window, t]: right edge closed for same-t callers
				k++
			}
		}
		if got != (k < 3) { // invariant 1: parity with the naive reference
			ok = false
		}
		if got {
			if len(hist) > 0 && t < prev { // invariant 3: monotone successes
				ok = false
			}
			prev, hist = t, append(hist, t)
		}
		var want []int64
		for _, ts := range hist {
			if ts >= t-10 { // naive reference keeps only non-expired entries
				want = append(want, ts)
			}
		}
		snap := c.l.Snapshot()
		if !reflect.DeepEqual(snap, want) { // invariant 2: retained set == in-window set
			ok = false
		}
		for _, ts := range snap {
			if ts < t-10 {
				ok = false
			}
		}
	}
	return ok && checkRejections()
}

// checkRejections verifies invariant 4 (rejected calls leave no trace),
// the three mutually distinct sentinel errors, and continued usability.
func checkRejections() bool {
	for _, bad := range [][2]int64{{0, 10}, {3, 0}, {-1, 10}} {
		if _, err := New(bad[0], bad[1]); !errors.Is(err, ErrInvalidConfig) {
			return false
		}
	}
	sentinels := []error{ErrInvalidConfig, lim.ErrNegativeTime, lim.ErrClockRewind}
	for i := range sentinels {
		for j := i + 1; j < len(sentinels); j++ {
			if errors.Is(sentinels[i], sentinels[j]) {
				return false
			}
		}
	}
	c, err := New(3, 10)
	if err != nil {
		return false
	}
	if a, err := c.Allow(5); err != nil || !a {
		return false
	}
	before := append([]int64(nil), c.l.Snapshot()...)
	n := c.Accepted()
	if _, err := c.Allow(-1); !errors.Is(err, lim.ErrNegativeTime) {
		return false
	}
	if _, err := c.Allow(4); !errors.Is(err, lim.ErrClockRewind) {
		return false
	}
	if c.Accepted() != n || !reflect.DeepEqual(c.l.Snapshot(), before) {
		return false
	}
	if a, err := c.Allow(5); err != nil || !a { // equal t is allowed, still usable
		return false
	}
	return true
}
