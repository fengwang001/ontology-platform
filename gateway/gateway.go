// Package gateway is an idempotent write gateway. Clients submit a write
// together with an idempotency key; the gateway guarantees the real
// execution runs at most once per accepted request, replays the stored
// outcome to duplicates, rejects conflicting bodies, allows retries after
// failures, and expires records by an injected clock.
//
// Concurrency: the gateway mutex is only held for short map operations.
// The execution function always runs outside any gateway lock, so a slow
// execution for one key never blocks submissions for other keys.
package gateway

import (
	"errors"
	"sync"
	"time"

	"ontology/digest"
	"ontology/record"
)

// ErrConflict is returned when a key is resubmitted with a different
// request body while its record is still live. Use errors.Is to test.
var ErrConflict = errors.New("gateway: idempotency key reused with a different request body")

// ExecFunc performs the real write. It is called at most once per
// accepted (key, body) pair.
type ExecFunc func(body []byte) ([]byte, error)

// Outcome is the result of a Submit call.
type Outcome struct {
	// Value is the bytes returned by the execution function.
	Value []byte
	// Replayed is false for the call that really executed, true for
	// duplicates that replayed the stored outcome.
	Replayed bool
}

// Info is a read-only snapshot of a key's record.
type Info struct {
	// Exists is false when the key is unknown or expired; all other
	// fields are then zero.
	Exists bool
	// State is the record's lifecycle state.
	State record.State
	// Remaining is the TTL left before expiry.
	Remaining time.Duration
	// HasResult reports whether a successful result is stored.
	HasResult bool
}

// Gateway manages idempotency records for many keys.
type Gateway struct {
	now func() time.Time
	ttl time.Duration

	mu        sync.Mutex
	records   map[string]*record.Record
	execCalls int
}

// New creates a Gateway. now is the injected clock — the only time source
// the gateway ever uses — and ttl is the record lifetime.
func New(now func() time.Time, ttl time.Duration) *Gateway {
	return &Gateway{
		now:     now,
		ttl:     ttl,
		records: make(map[string]*record.Record),
	}
}

// ExecCalls returns how many times the gateway has really invoked an
// execution function, across all keys.
func (g *Gateway) ExecCalls() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.execCalls
}

// Submit executes body under the given idempotency key, or replays the
// stored outcome when the key was already accepted with the same body.
//
//   - Same key, same body, first call still running: blocks until it
//     finishes and replays its outcome.
//   - Same key, same body, completed: replays the stored result.
//   - Same key, same body, failed: retries with a real execution.
//   - Same key, different body, record live: ErrConflict, nothing runs.
//   - Unknown or expired key: treated as brand new and really executed.
//
// A failed execution returns its error unchanged and is remembered as a
// failure; the key stays retryable until some attempt succeeds.
func (g *Gateway) Submit(key string, body []byte, exec ExecFunc) (Outcome, error) {
	fp := digest.Of(body)

	g.mu.Lock()
	now := g.now()
	rec, live := g.records[key]
	if live {
		switch rec.StateAt(now) {
		case record.StateExpired:
			delete(g.records, key)
			live = false
		case record.StateInFlight, record.StateCompleted:
			if !rec.Fingerprint().Equal(fp) {
				g.mu.Unlock()
				return Outcome{}, ErrConflict
			}
		case record.StateFailed:
			if !rec.Fingerprint().Equal(fp) {
				g.mu.Unlock()
				return Outcome{}, ErrConflict
			}
			delete(g.records, key)
			live = false
		}
	}

	if live {
		if rec.StateAt(now) == record.StateCompleted {
			res, _ := rec.Outcome()
			g.mu.Unlock()
			return Outcome{Value: res, Replayed: true}, nil
		}
		g.mu.Unlock()
		res, err := rec.Wait()
		return Outcome{Value: res, Replayed: true}, err
	}

	rec = record.New(fp)
	g.records[key] = rec
	g.execCalls++
	g.mu.Unlock()

	res, err := exec(body)
	rec.Complete(res, err, g.now().Add(g.ttl))
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{Value: res}, nil
}

// Inspect returns a read-only snapshot of key's record. Unknown or
// expired keys yield the zero Info with Exists false.
func (g *Gateway) Inspect(key string) Info {
	g.mu.Lock()
	defer g.mu.Unlock()
	rec, ok := g.records[key]
	if !ok {
		return Info{}
	}
	now := g.now()
	state := rec.StateAt(now)
	if state == record.StateExpired {
		delete(g.records, key)
		return Info{}
	}
	return Info{
		Exists:    true,
		State:     state,
		Remaining: rec.Remaining(now),
		HasResult: state == record.StateCompleted,
	}
}
