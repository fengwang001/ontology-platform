// Package api is the public entry point of the in-memory token-bucket rate
// limiter. It validates configuration and delegates to lim (which in turn
// owns the tkn bucket); the dependency direction is api -> lim -> tkn only.
package api

import (
	"errors"
	"fmt"

	"ontology/lim"
)

// ErrInvalidConfig is the distinct sentinel for a bad New configuration.
// It differs from lim.ErrInvalidNeed and lim.ErrClockRollback.
var ErrInvalidConfig = errors.New("api: capacity and rate must both be >= 1")

// Limiter is a single-bucket limiter driven by an explicit monotonic clock.
type Limiter struct {
	inner    *lim.Limiter
	capacity int64
	rate     int64
}

// New returns a full bucket with the given capacity and refill rate.
// capacity < 1 or rate < 1 yields ErrInvalidConfig.
func New(capacity, rate int64) (*Limiter, error) {
	if capacity < 1 || rate < 1 {
		return nil, ErrInvalidConfig
	}
	return &Limiter{inner: lim.New(capacity, rate), capacity: capacity, rate: rate}, nil
}

// Allow reports whether a request needing `need` tokens at timestamp t is
// admitted. need <= 0 -> lim.ErrInvalidNeed; t < last -> lim.ErrClockRollback;
// both leave the limiter untouched. Insufficient tokens is a normal (false, nil).
func (l *Limiter) Allow(t, need int64) (bool, error) { return l.inner.Allow(t, need) }

// Tokens returns the current token count.
func (l *Limiter) Tokens() int64 { return l.inner.Tokens() }

type scCase struct {
	t, need int64
	wantErr error // nil means a valid request
}

// selfCheckSequence is the built-in scenario: the eight-step worked example
// with all three precondition failures interleaved and two trailing requests.
func selfCheckSequence() []scCase {
	return []scCase{
		{0, 15, nil}, {0, 8, nil}, {3, 10, nil}, {3, 5, nil},
		{8, 12, nil}, {9, 6, nil}, {20, 18, nil}, {20, 3, nil},
		{20, 0, lim.ErrInvalidNeed}, {19, 1, lim.ErrClockRollback},
		{0, -5, lim.ErrInvalidNeed}, {20, 2, nil}, {25, 9, nil},
	}
}

// runOnce plays the sequence against a fresh limiter, comparing every
// decision, error and post-call token count with the naive reference.
func runOnce(capacity, rate int64, cases []scCase) ([]string, error) {
	l, err := New(capacity, rate)
	if err != nil {
		return nil, err
	}
	trace := make([]string, 0, len(cases))
	ref, prevT := capacity, int64(0) // independent naive reference
	for i, c := range cases {
		allowed, gErr := l.Allow(c.t, c.need)
		if c.wantErr != nil {
			if !errors.Is(gErr, c.wantErr) {
				return nil, fmt.Errorf("case %d: want error %v, got %v", i, c.wantErr, gErr)
			}
			// A failed request updates neither ref nor prevT (no trace).
		} else {
			if gErr != nil {
				return nil, fmt.Errorf("case %d: unexpected error %v", i, gErr)
			}
			ref = min(capacity, ref+(c.t-prevT)*rate)
			prevT = c.t
			want := ref >= c.need
			if allowed != want {
				return nil, fmt.Errorf("case %d: want allow=%v", i, want)
			}
			if allowed {
				ref -= c.need
			}
		}
		got := l.Tokens()
		if got != ref { // pins invariant 1 (reference) and 4 (no trace)
			return nil, fmt.Errorf("case %d: tokens=%d, naive ref=%d", i, got, ref)
		}
		if got < 0 || got > capacity { // pins invariant 2 (bounds)
			return nil, fmt.Errorf("case %d: tokens %d out of bounds", i, got)
		}
		trace = append(trace, fmt.Sprintf("%t|%v|%d", allowed, gErr, got))
	}
	return trace, nil
}

// SelfCheck replays a built-in request scenario on fresh limiters and
// verifies: naive-reference equivalence, token bounds, determinism (two
// runs give identical traces), and that failed requests leave no trace.
// It never mutates the receiver.
func (l *Limiter) SelfCheck() error {
	cs := selfCheckSequence()
	trace1, err := runOnce(l.capacity, l.rate, cs)
	if err != nil {
		return err
	}
	trace2, err := runOnce(l.capacity, l.rate, cs) // determinism: identical replay
	if err != nil {
		return err
	}
	for i := range trace1 {
		if trace1[i] != trace2[i] { // pins invariant 3 (determinism)
			return fmt.Errorf("non-deterministic result at case %d", i)
		}
	}
	return nil
}
