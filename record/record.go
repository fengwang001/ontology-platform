// Package record implements the state machine of a single idempotency
// key: in-flight, completed, failed, or expired. It stores the request
// fingerprint and the outcome of the execution, and lets concurrent
// callers wait for an in-flight execution to finish.
//
// A record never calls the wall clock itself: every time-dependent
// decision takes the current time as a parameter, supplied by the
// caller's injected clock.
package record

import (
	"sync"
	"time"

	"ontology/digest"
)

// State is the lifecycle state of one idempotency key.
type State int

const (
	// StateInFlight means the execution for this key is still running.
	StateInFlight State = iota
	// StateCompleted means the execution succeeded and the result is fixed.
	StateCompleted
	// StateFailed means the execution returned an error; the key may be retried.
	StateFailed
	// StateExpired means the record outlived its TTL and is treated as absent.
	StateExpired
)

// String renders the state for logging and tests.
func (s State) String() string {
	switch s {
	case StateInFlight:
		return "in-flight"
	case StateCompleted:
		return "completed"
	case StateFailed:
		return "failed"
	case StateExpired:
		return "expired"
	default:
		return "unknown"
	}
}

// Record is the state and stored outcome of one idempotency key.
// It is safe for concurrent use.
type Record struct {
	mu      sync.Mutex
	changed *sync.Cond

	fp      digest.Digest
	state   State
	value   any
	err     error
	created time.Time
	expires time.Time
}

// New creates an in-flight record for a request fingerprint. The record
// lives in [created, expires): at now == expires it is already expired.
func New(fp digest.Digest, created, expires time.Time) *Record {
	r := &Record{
		fp:      fp,
		state:   StateInFlight,
		created: created,
		expires: expires,
	}
	r.changed = sync.NewCond(&r.mu)
	return r
}

// Fingerprint returns the fingerprint of the request body that opened
// this record.
func (r *Record) Fingerprint() digest.Digest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fp
}

// MatchesBody reports whether fp equals the stored request fingerprint.
func (r *Record) MatchesBody(fp digest.Digest) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fp.Equal(fp)
}

// StateAt returns the effective state at now. Expiry is derived, never
// stored: an in-flight record never expires, and a finished record is
// expired exactly when now >= expires (left-closed, right-open).
func (r *Record) StateAt(now time.Time) State {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state == StateInFlight {
		return StateInFlight
	}
	if !now.Before(r.expires) {
		return StateExpired
	}
	return r.state
}

// Remaining returns the time until expiry at now, or zero if expired.
func (r *Record) Remaining(now time.Time) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	if d := r.expires.Sub(now); d > 0 {
		return d
	}
	return 0
}

// HasResult reports whether an execution outcome (success or failure)
// has been stored.
func (r *Record) HasResult() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state == StateCompleted || r.state == StateFailed
}

// Finish stores the execution outcome and wakes every waiter. A nil
// err completes the record; a non-nil err marks it failed.
func (r *Record) Finish(value any, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != StateInFlight {
		panic("record: Finish called on a finished record")
	}
	r.value = value
	r.err = err
	if err != nil {
		r.state = StateFailed
	} else {
		r.state = StateCompleted
	}
	r.changed.Broadcast()
}

// Await blocks until the record leaves the in-flight state, then
// returns the stored outcome exactly as Finish recorded it.
func (r *Record) Await() (value any, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for r.state == StateInFlight {
		r.changed.Wait()
	}
	return r.value, r.err
}
