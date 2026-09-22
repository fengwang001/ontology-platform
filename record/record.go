// Package record models the lifecycle of a single idempotency key.
//
// A Record is a small state machine:
//
//	InFlight  -- execution started, result not yet known
//	Completed -- execution succeeded, result fixed forever
//	Failed    -- execution failed, result kept but retry is allowed
//	Expired   -- derived state: past its deadline (never while InFlight)
//
// The record never reads a clock itself; callers inject the current time
// into StateAt/Remaining so the gateway stays the single owner of time.
//
// Synchronization is the caller's job: the gateway mutates records while
// holding its own mutex, and Waiters synchronize with Complete/Fail
// through the done channel (close happens-before observe).
package record

import (
	"time"

	"ontology/digest"
)

// State is the lifecycle state of one idempotency key.
type State int

const (
	// InFlight means an execution is currently running for this key.
	InFlight State = iota
	// Completed means the execution succeeded and the result is final.
	Completed
	// Failed means the execution returned an error; retry is allowed.
	Failed
	// Expired means the record outlived its deadline and must be
	// treated as if it never existed. Never applies to InFlight records.
	Expired
)

// String renders the state for logs, tests and the demo program.
func (s State) String() string {
	switch s {
	case InFlight:
		return "in-flight"
	case Completed:
		return "completed"
	case Failed:
		return "failed"
	case Expired:
		return "expired"
	default:
		return "unknown"
	}
}

// Record holds everything known about one idempotency key: the
// fingerprint of the accepted request body, the outcome of the
// execution, and the expiry deadline.
type Record struct {
	fp        digest.Fingerprint
	state     State
	value     any
	err       error
	expiresAt time.Time
	done      chan struct{}
}

// New creates an InFlight record for a request body fingerprint.
// expiresAt is the moment (on the injected clock) at which the finished
// record stops being replayable; it never interrupts an InFlight record.
func New(fp digest.Fingerprint, expiresAt time.Time) *Record {
	return &Record{
		fp:        fp,
		state:     InFlight,
		expiresAt: expiresAt,
		done:      make(chan struct{}),
	}
}

// Matches reports whether fp is the fingerprint of the accepted body.
func (r *Record) Matches(fp digest.Fingerprint) bool {
	return r.fp.Equal(fp)
}

// StateAt reports the state as of the injected now. Expiry is
// left-closed/right-open: now == expiresAt already counts as expired.
// An InFlight record is never expired.
func (r *Record) StateAt(now time.Time) State {
	if r.state == InFlight {
		return InFlight
	}
	if !now.Before(r.expiresAt) {
		return Expired
	}
	return r.state
}

// Complete stores a successful result and releases all waiters.
// Must be called exactly once, while InFlight.
func (r *Record) Complete(value any) {
	r.value = value
	r.state = Completed
	close(r.done)
}

// Fail stores the execution error and releases all waiters.
// Must be called exactly once, while InFlight.
func (r *Record) Fail(err error) {
	r.err = err
	r.state = Failed
	close(r.done)
}

// Wait blocks until the record leaves InFlight, then returns the stored
// outcome. Concurrent duplicates of an in-progress submission use this
// to coalesce onto the first execution instead of running their own.
func (r *Record) Wait() (any, error) {
	<-r.done
	return r.value, r.err
}

// Result returns the stored outcome. Callers must ensure the record is
// no longer InFlight (e.g. by holding the gateway mutex).
func (r *Record) Result() (any, error) {
	return r.value, r.err
}

// HasResult reports whether an execution outcome (success or failure)
// has been stored.
func (r *Record) HasResult() bool {
	return r.state == Completed || r.state == Failed
}

// Remaining returns the time-to-live left at now, clamped at zero.
func (r *Record) Remaining(now time.Time) time.Duration {
	d := r.expiresAt.Sub(now)
	if d < 0 {
		return 0
	}
	return d
}
