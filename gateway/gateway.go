// Package gateway is an idempotent write gateway. Clients submit a
// write together with an idempotency key; the gateway guarantees the
// execution function runs at most once per accepted request, replays
// the stored outcome for duplicates, rejects conflicting bodies, joins
// concurrent duplicates onto the in-flight execution, and expires
// records after a TTL measured on an injected clock.
//
// The gateway never reads the wall clock directly: all time comes from
// the now function injected at construction.
package gateway

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"ontology/digest"
	"ontology/record"
)

// ErrConflict is returned (wrapped) when a key is resubmitted with a
// different request body than the one that opened its record.
var ErrConflict = errors.New("gateway: request body conflicts with idempotency key")

// ExecFunc performs the actual write. It is invoked at most once per
// accepted request, outside any gateway lock.
type ExecFunc func() (any, error)

// Result is the outcome of a Submit call.
type Result struct {
	// Value is exactly what the execution function returned.
	Value any
	// Replayed is true when the outcome was replayed from a stored or
	// in-flight record instead of running the execution function.
	Replayed bool
}

// Status is a read-only snapshot of one key, returned by Query.
type Status struct {
	State     record.State
	Remaining time.Duration
	HasResult bool
	Exists    bool
}

// Gateway manages idempotency records for many keys. It is safe for
// concurrent use; the internal map lock is never held while the
// execution function runs, so one slow key cannot block others.
type Gateway struct {
	now func() time.Time
	ttl time.Duration

	mu      sync.Mutex
	records map[string]*record.Record
	execN   atomic.Int64
}

// New creates a gateway whose records live for ttl, with all time
// decisions made against the injected now clock.
func New(now func() time.Time, ttl time.Duration) *Gateway {
	if now == nil {
		panic("gateway: now clock must not be nil")
	}
	if ttl <= 0 {
		panic("gateway: ttl must be positive")
	}
	return &Gateway{now: now, ttl: ttl, records: make(map[string]*record.Record)}
}

// Submit runs exec exactly once for a (key, body) pair and replays the
// stored outcome for duplicates. A different body under the same key
// fails with ErrConflict without executing or overwriting anything.
// The execution error, if any, is returned as-is; failed keys may be
// retried, and the first success fixes the outcome for good.
func (g *Gateway) Submit(key string, body []byte, exec ExecFunc) (Result, error) {
	fp := digest.Of(body)
	rec, fresh := g.acquire(key, fp)
	if !fresh {
		if !rec.MatchesBody(fp) {
			return Result{}, fmt.Errorf("%w: key %q", ErrConflict, key)
		}
		value, err := rec.Await()
		return Result{Value: value, Replayed: true}, err
	}
	g.execN.Add(1)
	value, err := exec()
	rec.Finish(value, err)
	if err != nil {
		return Result{}, err
	}
	return Result{Value: value}, nil
}

// acquire returns the live record for key, creating a fresh in-flight
// one when the key is absent, expired, or previously failed. The
// second return value reports whether the caller owns the fresh record
// and must therefore run the execution function.
func (g *Gateway) acquire(key string, fp digest.Digest) (*record.Record, bool) {
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	if rec, ok := g.records[key]; ok {
		switch rec.StateAt(now) {
		case record.StateInFlight, record.StateCompleted:
			return rec, false
		}
		// Failed or expired: replace with a brand-new record.
	}
	rec := record.New(fp, now, now.Add(g.ttl))
	g.records[key] = rec
	return rec, true
}

// ExecCount returns how many times the gateway actually invoked an
// execution function, across all keys.
func (g *Gateway) ExecCount() int64 {
	return g.execN.Load()
}

// Query reports the current status of key. Missing or expired keys
// report the zero Status with Exists == false; no residual state leaks.
func (g *Gateway) Query(key string) Status {
	now := g.now()
	g.mu.Lock()
	rec, ok := g.records[key]
	g.mu.Unlock()
	if !ok {
		return Status{}
	}
	state := rec.StateAt(now)
	if state == record.StateExpired {
		return Status{}
	}
	return Status{
		State:     state,
		Remaining: rec.Remaining(now),
		HasResult: rec.HasResult(),
		Exists:    true,
	}
}
