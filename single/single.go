// Package single merges concurrent backend fetches for the same key
// into one execution (singleflight). Failures are never cached, and
// callers may cancel their own wait via context.
package single

import (
	"context"
	"errors"
	"sync"

	"ontology/entry"
)

// ErrInflightFull is returned when the number of distinct in-flight
// fetch keys reaches the configured limit. Joining an already
// in-flight key is always allowed and never returns this error.
var ErrInflightFull = errors.New("single: too many in-flight fetches")

// FetchFunc loads one key from the backend.
type FetchFunc func(ctx context.Context) (entry.FetchResult, error)

// call is one in-flight fetch shared by all waiters of one key.
type call struct {
	done    chan struct{}
	res     entry.FetchResult
	err     error
	waiters int
}

// Group deduplicates concurrent fetches per key. The zero value is
// ready to use with no in-flight limit.
type Group struct {
	mu          sync.Mutex
	inflight    map[string]*call
	maxInflight int
	calls       uint64
}

// New creates a Group allowing at most maxInflight distinct keys to
// fetch concurrently. maxInflight <= 0 means unbounded.
func New(maxInflight int) *Group {
	return &Group{inflight: make(map[string]*call), maxInflight: maxInflight}
}

// Do executes fn for key, merging concurrent callers of the same key
// into a single execution. Every waiter of the shared call observes
// the same result. A failed call is removed immediately, so the next
// Do re-executes fn: failures are never cached.
func (g *Group) Do(ctx context.Context, key string, fn FetchFunc) (entry.FetchResult, error) {
	g.mu.Lock()
	if c, ok := g.inflight[key]; ok {
		c.waiters++
		g.mu.Unlock()
		return g.wait(ctx, c)
	}
	if g.maxInflight > 0 && len(g.inflight) >= g.maxInflight {
		g.mu.Unlock()
		return entry.FetchResult{}, ErrInflightFull
	}
	c := &call{done: make(chan struct{}), waiters: 1}
	g.inflight[key] = c
	g.calls++
	g.mu.Unlock()

	c.res, c.err = fn(ctx)

	g.mu.Lock()
	delete(g.inflight, key)
	close(c.done)
	g.mu.Unlock()
	return c.res, c.err
}

// wait blocks until the shared call finishes or ctx is cancelled.
// Cancellation only abandons the wait; the shared call keeps running
// for the remaining waiters.
func (g *Group) wait(ctx context.Context, c *call) (entry.FetchResult, error) {
	select {
	case <-c.done:
		return c.res, c.err
	case <-ctx.Done():
		g.mu.Lock()
		c.waiters--
		g.mu.Unlock()
		return entry.FetchResult{}, ctx.Err()
	}
}

// Calls returns how many times a fetch function was actually
// executed (i.e. how many distinct flights happened).
func (g *Group) Calls() uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

// Inflight returns how many distinct keys are currently fetching.
func (g *Group) Inflight() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.inflight)
}

// Waiting returns how many goroutines share the in-flight call for
// key, or 0 when no call for key is in flight.
func (g *Group) Waiting(key string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if c, ok := g.inflight[key]; ok {
		return c.waiters
	}
	return 0
}
