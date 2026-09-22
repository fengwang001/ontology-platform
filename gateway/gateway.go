// Package gateway is the write gateway: it deduplicates submissions by
// idempotency key, executes each key at most once per fixed result,
// replays stored outcomes, and detects body conflicts via fingerprints.
package gateway

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"ontology/digest"
	"ontology/record"
)

// ErrConflict marks a same-key/different-body submission.
var ErrConflict = errors.New("gateway: request body conflicts with idempotency key")

// ConflictError is returned when a key is resubmitted with a different
// request body. It unwraps to ErrConflict.
type ConflictError struct {
	Key string
}

// Error implements error.
func (e *ConflictError) Error() string {
	return ErrConflict.Error() + ": " + e.Key
}

// Unwrap supports errors.Is(err, ErrConflict).
func (e *ConflictError) Unwrap() error {
	return ErrConflict
}

// ExecuteFunc performs the real write. It is invoked at most once per
// fixed (successful or in-flight) record.
type ExecuteFunc func(ctx context.Context, body []byte) ([]byte, error)

// Outcome is what Submit returns to the caller.
type Outcome struct {
	Result   []byte
	Err      error
	Replayed bool // true when the outcome came from a stored record
}

// Status is a read-only snapshot of one key. The zero value means the
// key does not exist (or has expired).
type Status struct {
	Exists    bool
	State     record.State
	Remaining time.Duration
	HasResult bool
}

// Gateway manages many idempotency keys. All time comes from the
// injected clock; no wall-clock calls are made internally.
type Gateway struct {
	now func() time.Time
	ttl time.Duration

	mu      sync.Mutex
	records map[string]*record.Record
	execs   atomic.Int64
}

// New creates a Gateway with the injected clock and record TTL.
func New(now func() time.Time, ttl time.Duration) *Gateway {
	return &Gateway{
		now:     now,
		ttl:     ttl,
		records: make(map[string]*record.Record),
	}
}

// ExecCount reports how many times the gateway actually invoked an
// ExecuteFunc. Replays and conflicts never increment it.
func (g *Gateway) ExecCount() int64 {
	return g.execs.Load()
}

// Submit executes or replays the write for (key, body).
//
// Semantics: same key+body executes once and replays afterwards; same
// key+different body fails with *ConflictError without executing; a
// failed execution may be retried with the same key; an in-flight
// same-body submission waits for the first result.
func (g *Gateway) Submit(ctx context.Context, key string, body []byte, exec ExecuteFunc) Outcome {
	fp := digest.Of(body)
	now := g.now()

	g.mu.Lock()
	rec, ok := g.records[key]
	if ok && rec.Expired(now) {
		delete(g.records, key)
		ok = false
	}
	if ok {
		g.mu.Unlock()
		return g.join(rec, fp, key)
	}
	rec = record.New(fp, now, g.ttl)
	g.records[key] = rec
	g.mu.Unlock()

	g.execs.Add(1)
	result, err := exec(ctx, body)
	rec.Complete(result, err)
	if err != nil {
		// Failures are remembered for in-flight joiners but do not
		// block a later retry with the same key.
		g.mu.Lock()
		if g.records[key] == rec {
			delete(g.records, key)
		}
		g.mu.Unlock()
	}
	return Outcome{Result: result, Err: err}
}

// join attaches to an existing record: conflict on body mismatch,
// otherwise wait for the first execution and replay its outcome.
func (g *Gateway) join(rec *record.Record, fp digest.Fingerprint, key string) Outcome {
	if !rec.Fingerprint().Equal(fp) {
		return Outcome{Err: &ConflictError{Key: key}}
	}
	rec.Wait()
	result, err, _ := rec.Result()
	return Outcome{Result: result, Err: err, Replayed: true}
}

// Inspect returns a read-only snapshot of key. Missing or expired keys
// yield the zero Status.
func (g *Gateway) Inspect(key string) Status {
	now := g.now()

	g.mu.Lock()
	rec, ok := g.records[key]
	if ok && rec.Expired(now) {
		delete(g.records, key)
		ok = false
	}
	g.mu.Unlock()
	if !ok {
		return Status{}
	}

	_, _, state := rec.Result()
	return Status{
		Exists:    true,
		State:     state,
		Remaining: rec.Remaining(now),
		HasResult: state == record.StateCompleted || state == record.StateFailed,
	}
}
