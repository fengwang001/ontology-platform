// Package record models the lifecycle of a single idempotency key:
// in-flight, completed, failed, or expired. It stores the request-body
// fingerprint and the outcome of the one real execution, and lets
// concurrent duplicate submissions wait for that outcome.
//
// A record never touches the wall clock itself: every state query takes
// the current time as an argument, supplied by the caller's injected clock.
package record

import (
	"sync"
	"time"

	"ontology/digest"
)

// State is the lifecycle state of a record.
type State int

const (
	// StateUnknown is the zero value; no usable record exists.
	StateUnknown State = iota
	// StateInFlight means the first execution is still running.
	StateInFlight
	// StateCompleted means the execution succeeded and the result is fixed.
	StateCompleted
	// StateFailed means the execution returned an error; the key may be retried.
	StateFailed
	// StateExpired means the record outlived its TTL and is treated as absent.
	StateExpired
)

// String renders the state for logs and demos.
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

// Record is the state machine and result slot for one idempotency key.
//
// A Record is created in StateInFlight. Exactly one goroutine — the one
// running the real execution — must call Complete, once. Concurrent
// duplicates use Wait to block until that happens.
type Record struct {
	fp   digest.Digest
	done chan struct{}

	mu        sync.Mutex
	state     State
	result    []byte
	execErr   error
	expiresAt time.Time
}

// New creates an in-flight record bound to the given body fingerprint.
func New(fp digest.Digest) *Record {
	return &Record{
		fp:    fp,
		done:  make(chan struct{}),
		state: StateInFlight,
	}
}

// Fingerprint returns the request-body fingerprint stored at creation.
func (r *Record) Fingerprint() digest.Digest {
	return r.fp
}

// StateAt reports the state as of now. Expiry is half-open: a record is
// expired exactly when now >= expiresAt. An in-flight record never expires.
func (r *Record) StateAt(now time.Time) State {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state == StateInFlight {
		return StateInFlight
	}
	if !r.expiresAt.After(now) {
		return StateExpired
	}
	return r.state
}

// Remaining returns the TTL left as of now, or zero when expired.
func (r *Record) Remaining(now time.Time) time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state == StateInFlight {
		return 0
	}
	if d := r.expiresAt.Sub(now); d > 0 {
		return d
	}
	return 0
}

// Complete stores the execution outcome and releases all waiters.
// expiresAt is computed by the caller from its injected clock.
// It must be called exactly once, by the executing goroutine only.
func (r *Record) Complete(result []byte, execErr error, expiresAt time.Time) {
	r.mu.Lock()
	if execErr != nil {
		r.state = StateFailed
	} else {
		r.state = StateCompleted
	}
	r.result = result
	r.execErr = execErr
	r.expiresAt = expiresAt
	r.mu.Unlock()
	close(r.done)
}

// Wait blocks until the in-flight execution finishes, then returns the
// stored outcome. It must not be called on a record that has already
// left StateInFlight; use Outcome instead.
func (r *Record) Wait() ([]byte, error) {
	<-r.done
	return r.Outcome()
}

// Outcome returns the stored result and error of a finished record.
func (r *Record) Outcome() ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.result, r.execErr
}
