// Package idem implements an idempotency-key executor with single-flight
// semantics, fingerprint protection and TTL-based record expiry driven by an
// injectable clock.
package idem

import (
	"sync"
	"time"
)

// Executor serializes concurrent calls sharing an idempotency key and caches
// completed results until they expire.
type Executor struct {
	mu   sync.Mutex
	slot map[string]*entry

	now func() time.Time
	ttl time.Duration
}

// Result is the observable outcome of executing a request. It is returned to
// every caller sharing the same idempotency key and request fingerprint.
type Result struct {
	Code int
	Body string
}

// Outcome describes how the result was obtained by a particular caller.
type Outcome int

const (
	// Executed means fn ran in the calling goroutine.
	Executed Outcome = iota
	// Replayed means a cached result from a previous call was returned; fn
	// did not run.
	Replayed
	// Waited means another concurrent caller ran fn and this caller blocked
	// until it finished, receiving the shared result.
	Waited
)

func (o Outcome) String() string {
	switch o {
	case Executed:
		return "Executed"
	case Replayed:
		return "Replayed"
	case Waited:
		return "Waited"
	default:
		return "Unknown"
	}
}

// New creates an Executor. now is the clock used for expiry checks; ttl is the
// lifetime of a completed record measured from its completion instant. A
// non-positive ttl means records never expire.
func New(now func() time.Time, ttl time.Duration) *Executor {
	if now == nil {
		now = time.Now
	}
	return &Executor{
		slot: make(map[string]*entry),
		now:  now,
		ttl:  ttl,
	}
}
