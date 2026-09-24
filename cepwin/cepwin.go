// Package cepwin holds the window predicate, expiry predicate and input
// validation for the CEP pattern "A -> B within T". It depends on nothing.
package cepwin

import "errors"

// Mode selects strict or relaxed contiguity.
type Mode int

const (
	Strict Mode = iota + 1
	Relaxed
)

// Event is one upstream event, fed in arrival order.
type Event struct {
	Key  string
	Type string
	TS   int64
}

// Sentinel errors: every rejection path maps to exactly one of these so
// callers can decide with errors.Is. All six are pairwise distinct.
var (
	ErrNegativeT      = errors.New("cepwin: T must not be negative")
	ErrInvalidPending = errors.New("cepwin: maxPending must be positive")
	ErrInvalidMode    = errors.New("cepwin: mode must be Strict or Relaxed")
	ErrInvalidEvent   = errors.New("cepwin: event Key and Type must be non-empty")
	ErrTimeRegression = errors.New("cepwin: TS went backwards for the same Key")
	ErrQueueFull      = errors.New("cepwin: pending-A queue reached maxPending")
)

// InWindow reports whether 0 <= bTS-aTS <= t (closed interval, equality
// with t still matches).
func InWindow(aTS, bTS, t int64) bool {
	d := bTS - aTS
	return d >= 0 && d <= t
}

// Expired reports whether an A at aTS can never match an event at now:
// now-aTS > t. Per-Key TS are non-decreasing so once expired an A is
// expired forever and must be dropped from the head of the queue.
func Expired(aTS, now, t int64) bool { return now-aTS > t }

// ValidateEvent rejects events with an empty Key or Type.
func ValidateEvent(ev Event) error {
	if ev.Key == "" || ev.Type == "" {
		return ErrInvalidEvent
	}
	return nil
}

// ValidateParams checks the constructor parameters. The order is fixed:
// T first, then maxPending, then mode, so the reported error is stable.
func ValidateParams(mode Mode, t int64, maxPending int) error {
	if t < 0 {
		return ErrNegativeT
	}
	if maxPending <= 0 {
		return ErrInvalidPending
	}
	if mode != Strict && mode != Relaxed {
		return ErrInvalidMode
	}
	return nil
}
