// Package gateway is a write gateway with idempotency keys.
//
// Clients submit a write together with an idempotency key. The gateway
// guarantees:
//
//   - same key + same body: the execute function runs exactly once,
//     later submissions replay the stored result;
//   - same key + different body: a conflict error, nothing executed,
//     nothing overwritten;
//   - concurrent duplicates while the first execution is still running
//     coalesce onto that execution and wait for its result;
//   - failed executions are remembered (and returned as-is) but may be
//     retried with the same key and body; once a submission succeeds,
//     the success is final;
//   - records expire after a TTL measured on an injected clock, and an
//     expired key is treated as brand new.
//
// All time comes from the injected now func; the package never calls
// time.Now itself. The gateway is safe for concurrent use, and a slow
// execution for one key never blocks submissions for other keys.
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
// request body whose fingerprint differs from the accepted one.
// Detect it with errors.Is(err, ErrConflict).
var ErrConflict = errors.New("gateway: request body conflicts with existing idempotency key")

// Result is the outcome of a Submit call.
type Result struct {
	// Value is whatever the execute function returned. For a replay it
	// is the exact value stored by the first execution.
	Value any
	// Replayed is false for the submission that actually ran the
	// execute function, and true for every replay of a stored result.
	Replayed bool
}

// Status is a read-only snapshot of one key, returned by Lookup.
type Status struct {
	State     record.State
	Remaining time.Duration
	HasResult bool
}

// Gateway manages idempotency records for many keys.
type Gateway struct {
	now func() time.Time
	ttl time.Duration

	mu      sync.Mutex
	records map[string]*record.Record

	execs atomic.Int64
}

// New creates a Gateway. now is the injected clock (the only time source
// the gateway uses); ttl is how long a finished record stays replayable.
func New(now func() time.Time, ttl time.Duration) *Gateway {
	return &Gateway{
		now:     now,
		ttl:     ttl,
		records: make(map[string]*record.Record),
	}
}

// ExecCount reports how many times an execute function was actually
// invoked across all keys. Replays never increment it.
func (g *Gateway) ExecCount() int64 {
	return g.execs.Load()
}

// Submit runs fn for (key, body) unless a stored record says otherwise.
//
// fn is never invoked while the gateway mutex is held, so a slow fn for
// one key cannot stall submissions for other keys.
func (g *Gateway) Submit(key string, body []byte, fn func() (any, error)) (Result, error) {
	fp := digest.Of(body)

	g.mu.Lock()
	if rec, ok := g.records[key]; ok {
		switch rec.StateAt(g.now()) {
		case record.InFlight:
			if !rec.Matches(fp) {
				g.mu.Unlock()
				return Result{}, conflict(key)
			}
			g.mu.Unlock()
			// Coalesce onto the running execution: wait for the exact
			// outcome of the first submission instead of running again.
			value, err := rec.Wait()
			return Result{Value: value, Replayed: true}, err
		case record.Completed:
			if !rec.Matches(fp) {
				g.mu.Unlock()
				return Result{}, conflict(key)
			}
			value, _ := rec.Result()
			g.mu.Unlock()
			return Result{Value: value, Replayed: true}, nil
		case record.Failed:
			if !rec.Matches(fp) {
				g.mu.Unlock()
				return Result{}, conflict(key)
			}
			// Failures are retryable: drop the failed record and fall
			// through to a fresh execution below.
			delete(g.records, key)
		case record.Expired:
			// An expired key is a brand-new request.
			delete(g.records, key)
		}
	}

	rec := record.New(fp, g.now().Add(g.ttl))
	g.records[key] = rec
	g.mu.Unlock()

	g.execs.Add(1)
	value, err := fn()

	g.mu.Lock()
	if err != nil {
		rec.Fail(err)
	} else {
		rec.Complete(value)
	}
	g.mu.Unlock()

	if err != nil {
		return Result{}, err
	}
	return Result{Value: value}, nil
}

// Lookup returns a snapshot of the key's current state. The second
// return value is false — and the Status is the zero value — when the
// key is unknown or already expired; no stale state leaks out.
func (g *Gateway) Lookup(key string) (Status, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	rec, ok := g.records[key]
	if !ok {
		return Status{}, false
	}
	now := g.now()
	state := rec.StateAt(now)
	if state == record.Expired {
		return Status{}, false
	}
	return Status{
		State:     state,
		Remaining: rec.Remaining(now),
		HasResult: rec.HasResult(),
	}, true
}

func conflict(key string) error {
	return fmt.Errorf("%w: key %q", ErrConflict, key)
}
