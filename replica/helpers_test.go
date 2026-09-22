package replica

import (
	"context"
	"sync"
	"testing"
	"time"

	"ontology/entry"
	"ontology/version"
)

// manualClock is an injected, test-controlled time source.
type manualClock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *manualClock { return &manualClock{now: time.Unix(1_000_000, 0)} }

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// backend is an in-memory "source of truth" with versioned keys.
type backend struct {
	mu     sync.Mutex
	alloc  *version.Allocator
	vals   map[string]any
	vers   map[string]version.Version
	calls  int
	hook   func(key string) // optional, runs inside load (for races)
	fail   error
	absent map[string]bool
}

func newBackend() *backend {
	return &backend{
		alloc:  version.NewAllocator(),
		vals:   map[string]any{},
		vers:   map[string]version.Version{},
		absent: map[string]bool{},
	}
}

// set writes val under key and returns the new backend version.
func (b *backend) set(key string, val any) version.Version {
	b.mu.Lock()
	defer b.mu.Unlock()
	v := b.alloc.Next()
	b.vals[key] = val
	b.vers[key] = v
	delete(b.absent, key)
	return v
}

func (b *backend) loadCalls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

// load is the Loader implementation.
func (b *backend) load(_ context.Context, key string) (entry.FetchResult, error) {
	if b.hook != nil {
		b.hook(key)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	if b.fail != nil {
		return entry.FetchResult{}, b.fail
	}
	v, ok := b.vers[key]
	if !ok {
		// The absence fact itself is versioned by the backend.
		v = b.alloc.Next()
		b.vers[key] = v
		b.absent[key] = true
	}
	if b.absent[key] {
		return entry.FetchResult{Version: v, Found: false}, nil
	}
	return entry.FetchResult{Value: b.vals[key], Version: v, Found: true}, nil
}

// fixture wires a clock, a backend and a replica together.
type fixture struct {
	clock *manualClock
	be    *backend
	rep   *Replica
}

func newFixture(t *testing.T, ttl time.Duration, maxEntries int) *fixture {
	t.Helper()
	c := newClock()
	be := newBackend()
	r := New(Config{
		Loader:     be.load,
		Clock:      c.Now,
		TTL:        ttl,
		MaxEntries: maxEntries,
	})
	return &fixture{clock: c, be: be, rep: r}
}

func mustRead(t *testing.T, r *Replica, key string) ReadResult {
	t.Helper()
	res, err := r.Read(context.Background(), key)
	if err != nil {
		t.Fatalf("Read(%q): %v", key, err)
	}
	return res
}
