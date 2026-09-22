// Package record implements the per-key idempotency state machine:
// in-flight, completed, failed, expired. It depends only on digest.
package record

import (
	"sync"
	"time"

	"ontology/digest"
)

// State is the lifecycle state of a single idempotency key record.
type State int

const (
	// StateInFlight means the first execution is still running.
	StateInFlight State = iota
	// StateCompleted means execution finished successfully.
	StateCompleted
	// StateFailed means execution returned an error; retry is allowed.
	StateFailed
	// StateExpired means the record outlived its TTL (derived, not stored).
	StateExpired
)

// String renders the state for logs and tests.
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

// Record holds the state and outcome of one idempotency key.
// The zero value is not usable; construct with New.
type Record struct {
	fp        digest.Fingerprint
	expiresAt time.Time
	done      chan struct{}

	mu     sync.Mutex
	state  State
	result []byte
	err    error
}

// New creates an in-flight record for fp that expires at now+ttl.
func New(fp digest.Fingerprint, now time.Time, ttl time.Duration) *Record {
	return &Record{
		fp:        fp,
		expiresAt: now.Add(ttl),
		done:      make(chan struct{}),
		state:     StateInFlight,
	}
}

// Fingerprint returns the request-body fingerprint bound to this record.
func (r *Record) Fingerprint() digest.Fingerprint {
	return r.fp
}

// ExpiresAt returns the absolute expiry moment.
func (r *Record) ExpiresAt() time.Time {
	return r.expiresAt
}

// Expired reports whether the record is expired at now. The interval is
// left-closed right-open: now == expiresAt already counts as expired.
// In-flight records are never expired.
func (r *Record) Expired(now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state == StateInFlight {
		return false
	}
	return !now.Before(r.expiresAt)
}

// Complete stores the execution outcome and releases all waiters.
// It is a no-op unless the record is in-flight.
func (r *Record) Complete(result []byte, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state != StateInFlight {
		return
	}
	if err != nil {
		r.state = StateFailed
	} else {
		r.state = StateCompleted
	}
	r.result = result
	r.err = err
	close(r.done)
}

// Wait blocks until the first execution finishes.
func (r *Record) Wait() {
	<-r.done
}

// Result returns the stored outcome and current state.
func (r *Record) Result() (result []byte, err error, state State) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.result, r.err, r.state
}

// State returns the current stored state (never StateExpired).
func (r *Record) State() State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

// Remaining returns the TTL left at now, clamped at zero.
func (r *Record) Remaining(now time.Time) time.Duration {
	d := r.expiresAt.Sub(now)
	if d < 0 {
		return 0
	}
	return d
}
