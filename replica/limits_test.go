package replica

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/bus"
	"ontology/entry"
	"ontology/single"
)

// Requirement 10: the three limits fail with three distinguishable
// errors, and a rejected operation changes no state.
func TestLimitsAreDistinguishableAndAtomic(t *testing.T) {
	// Entry limit.
	f := newFixture(t, ttl, 2)
	f.be.set("a", 1)
	f.be.set("b", 2)
	mustRead(t, f.rep, "a")
	mustRead(t, f.rep, "b")
	beforeA, beforeB := f.rep.Inspect("a"), f.rep.Inspect("b")
	f.be.set("c", 3)
	_, errEntries := f.rep.Read(context.Background(), "c")
	if !errors.Is(errEntries, ErrEntriesFull) {
		t.Fatalf("err = %v", errEntries)
	}
	if f.rep.Inspect("a") != beforeA || f.rep.Inspect("b") != beforeB {
		t.Fatal("rejected read changed existing state")
	}
	if info := f.rep.Inspect("c"); info.State != entry.Hole {
		t.Fatalf("rejected key appeared: %+v", info)
	}

	// Singleflight limit.
	f2 := newFixture(t, ttl, 0)
	f2.rep.group = single.New(1)
	f2.be.set("slow", 1)
	started := make(chan struct{})
	release := make(chan struct{})
	f2.be.hook = func(key string) {
		if key == "slow" {
			close(started)
			<-release
		}
	}
	go func() { _, _ = f2.rep.Read(context.Background(), "slow") }()
	<-started
	f2.be.set("other", 2)
	_, errInflight := f2.rep.Read(context.Background(), "other")
	close(release)
	if !errors.Is(errInflight, single.ErrInflightFull) {
		t.Fatalf("err = %v", errInflight)
	}
	if info := f2.rep.Inspect("other"); info.State != entry.Hole {
		t.Fatalf("rejected key appeared: %+v", info)
	}

	// Bus queue limit.
	b := bus.New(1)
	_ = b.Publish(bus.Notification{Key: "x", Version: 1})
	errQueue := b.Publish(bus.Notification{Key: "y", Version: 2})
	if !errors.Is(errQueue, bus.ErrQueueFull) {
		t.Fatalf("err = %v", errQueue)
	}

	// All three errors are mutually distinguishable.
	if errors.Is(errEntries, errInflight) || errors.Is(errEntries, errQueue) ||
		errors.Is(errInflight, errEntries) || errors.Is(errInflight, errQueue) ||
		errors.Is(errQueue, errEntries) || errors.Is(errQueue, errInflight) {
		t.Fatal("limit errors are not distinguishable")
	}
}

// Requirement 10: eviction policy — when full, expired entries are
// evicted first to make room; live entries are never evicted.
func TestEvictionPrefersExpiredEntries(t *testing.T) {
	f := newFixture(t, ttl, 2)
	f.be.set("a", 1)
	f.be.set("b", 2)
	mustRead(t, f.rep, "a")
	mustRead(t, f.rep, "b")

	f.clock.Advance(ttl) // both entries expire
	f.be.set("c", 3)
	res := mustRead(t, f.rep, "c")
	if res.State != Hit || res.Value != 3 {
		t.Fatalf("read c = %+v", res)
	}
	if got := f.rep.Len(); got != 1 {
		t.Fatalf("len = %d, want 1 (expired evicted)", got)
	}
	if info := f.rep.Inspect("a"); info.State != entry.Hole {
		t.Fatalf("expired a not evicted: %+v", info)
	}
}

// Requirement 11: Inspect is stable and side-effect free beyond lazy
// expiry; unknown keys are the zero value.
func TestInspectIsStable(t *testing.T) {
	f := newFixture(t, ttl, 0)
	f.be.set("k", "v1")
	mustRead(t, f.rep, "k")
	mustRead(t, f.rep, "k")
	first := f.rep.Inspect("k")
	second := f.rep.Inspect("k")
	if first != second {
		t.Fatalf("inspect not stable: %+v vs %+v", first, second)
	}
	if first.Hits != 1 || first.Misses != 1 || first.Fetches != 1 {
		t.Fatalf("counters = %+v", first)
	}
	if got := f.be.loadCalls(); got != 1 {
		t.Fatalf("inspect triggered fetch: calls = %d", got)
	}
	zero := f.rep.Inspect("unknown")
	if zero != (KeyInfo{State: entry.Hole}) {
		t.Fatalf("unknown key = %+v", zero)
	}
}

// Requirement 12: concurrent reads, notifications and clock advances
// are race-free and the mid-flight invalidation rule still holds.
func TestConcurrentMixKeepsInvariants(t *testing.T) {
	clock := newClock()
	be := newBackend()
	rep := New(Config{Loader: be.load, Clock: clock.Now, TTL: ttl})
	be.set("k", "v1")

	const workers = 8
	const rounds = 200
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				switch (w + i) % 3 {
				case 0:
					if _, err := rep.Read(context.Background(), "k"); err != nil {
						t.Error(err)
					}
				case 1:
					v := be.set("k", fmt.Sprintf("w%d-i%d", w, i))
					_ = rep.ApplyNotification(bus.Notification{Key: "k", Version: v})
				case 2:
					clock.Advance(ttl / 4)
					rep.Inspect("k")
				}
			}
		}(w)
	}
	wg.Wait()

	// Final state must never hold a value whose version is older
	// than the newest notification the replica accepted.
	info := rep.Inspect("k")
	be.mu.Lock()
	latest := be.vers["k"]
	be.mu.Unlock()
	if info.State == entry.Valid && info.Version.Before(latest) {
		// Only acceptable right after an invalidation; force a read.
		res, err := rep.Read(context.Background(), "k")
		if err != nil {
			t.Fatal(err)
		}
		if res.State == Hit && res.Version.Before(latest) {
			t.Fatalf("serving stale version %v, backend at %v", res.Version, latest)
		}
	}
}
