// Package cepwin holds the pure window/expiry predicates and parameter and
// event validation for CEP pattern `A -> B within T`. It depends on nothing.
package cepwin

import "errors"

// Event is an upstream event fed to the matcher.
type Event struct {
	Key  string
	Type string
	TS   int64
}

// Match is a matched (A, B) pair; A arrived before B and both share Key.
type Match struct {
	A Event
	B Event
}

// Sentinel errors: every rejection is decidable via errors.Is.
var (
	// ErrNegativeT: window T must not be negative.
	ErrNegativeT = errors.New("cepwin: T must not be negative")
	// ErrNonPositiveMaxPending: maxPending must be >= 1.
	ErrNonPositiveMaxPending = errors.New("cepwin: maxPending must be positive")
	// ErrEmptyField: event Key and Type must both be non-empty.
	ErrEmptyField = errors.New("cepwin: event Key and Type must be non-empty")
)

// CheckWindow validates the window parameter.
func CheckWindow(T int64) error {
	if T < 0 {
		return ErrNegativeT
	}
	return nil
}

// CheckLimit validates the relaxed-mode pending queue capacity.
func CheckLimit(maxPending int) error {
	if maxPending <= 0 {
		return ErrNonPositiveMaxPending
	}
	return nil
}

// CheckParams validates the window and queue-capacity parameters together.
func CheckParams(T int64, maxPending int) error {
	if err := CheckWindow(T); err != nil {
		return err
	}
	return CheckLimit(maxPending)
}

// ValidateEvent rejects events with an empty Key or Type.
func ValidateEvent(ev Event) error {
	if ev.Key == "" || ev.Type == "" {
		return ErrEmptyField
	}
	return nil
}

// InWindow reports whether an A at aTS and a B at bTS satisfy the closed
// window condition 0 <= bTS-aTS <= T. Equal timestamps are allowed.
func InWindow(aTS, bTS, T int64) bool {
	d := bTS - aTS
	return d >= 0 && d <= T
}

// Expired reports whether a pending A at aTS can never match an event at
// nowTS any more: nowTS-aTS > T. Per-key timestamps are non-decreasing, so
// once true for the queue head it stays true.
func Expired(aTS, nowTS, T int64) bool {
	return nowTS-aTS > T
}
