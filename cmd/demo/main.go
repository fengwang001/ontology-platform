// Command demo exercises every semantic guarantee of the cache
// invalidation coordinator and prints one OK/FAIL line per check.
// It uses an injected clock only: no network, no filesystem, no
// real time.
package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"ontology/entry"
	"ontology/replica"
	"ontology/version"
)

const ttl = 10 * time.Second

// clock is the injected, hand-advanced time source.
type clock struct{ now time.Time }

func newClock() *clock          { return &clock{now: time.Unix(1_000_000, 0)} }
func (c *clock) Now() time.Time { return c.now }
func (c *clock) Advance(d time.Duration) {
	c.now = c.now.Add(d)
}

// backend is an in-memory versioned "source of truth".
type backend struct {
	mu    sync.Mutex
	alloc *version.Allocator
	vals  map[string]any
	vers  map[string]version.Version
	calls int
	fail  error
	hook  func(key string)
}

func newBackend() *backend {
	return &backend{
		alloc: version.NewAllocator(),
		vals:  map[string]any{},
		vers:  map[string]version.Version{},
	}
}

func (b *backend) set(key string, val any) version.Version {
	b.mu.Lock()
	defer b.mu.Unlock()
	v := b.alloc.Next()
	b.vals[key], b.vers[key] = val, v
	return v
}

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
		return entry.FetchResult{Version: b.alloc.Next(), Found: false}, nil
	}
	return entry.FetchResult{Value: b.vals[key], Version: v, Found: true}, nil
}

func (b *backend) loadCalls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

// fixture bundles a clock, a backend and a replica.
type fixture struct {
	clock *clock
	be    *backend
	rep   *replica.Replica
}

func newFixture(maxEntries int) *fixture {
	c := newClock()
	be := newBackend()
	r := replica.New(replica.Config{
		Loader:     be.load,
		Clock:      c.Now,
		TTL:        ttl,
		MaxEntries: maxEntries,
	})
	return &fixture{clock: c, be: be, rep: r}
}

func read(r *replica.Replica, key string) replica.ReadResult {
	res, _ := r.Read(context.Background(), key)
	return res
}

func main() {
	checks := []struct {
		name string
		run  func() bool
	}{
		{"out-of-order delivery converges (100 shuffles)", checkOutOfOrder},
		{"duplicate notification is idempotent", checkDuplicate},
		{"expiry boundary forces refetch", checkExpiry},
		{"concurrent miss merges into one fetch", checkSingleflight},
		{"slow key does not block other keys", checkSlowKey},
		{"failed fetch not cached, retry refetches", checkFailure},
		{"mid-flight invalidation discards stale result", checkMidFlight},
		{"hole vs absent are distinguishable", checkHoleAbsent},
		{"8 replicas converge after chaos delivery", checkConverge},
		{"notification probes constant (N=100 vs 10000)", checkProbeCount},
		{"three limits reject atomically, distinguishable", checkLimits},
		{"inspect is stable and side-effect free", checkInspect},
	}
	ok := 0
	for _, c := range checks {
		verdict := "OK  "
		if c.run() {
			ok++
		} else {
			verdict = "FAIL"
		}
		fmt.Printf("%s %s\n", verdict, c.name)
	}
	fmt.Printf("TOTAL %d/%d OK\n", ok, len(checks))
	if ok != len(checks) {
		os.Exit(1)
	}
}
