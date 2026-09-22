package replica

import (
	"context"
	"sync"
	"testing"

	"ontology/bus"
	"ontology/entry"
)

// Requirement 12 (targeted): the mid-flight discard of requirement 6
// holds when many readers and invalidators race on one key.
func TestConcurrentMidFlightInvalidation(t *testing.T) {
	for trial := 0; trial < 50; trial++ {
		clock := newClock()
		be := newBackend()
		rep := New(Config{Loader: be.load, Clock: clock.Now, TTL: ttl})
		be.set("k", "old")
		gate := make(chan struct{})
		be.hook = func(string) { <-gate }

		const readers = 4
		var wg sync.WaitGroup
		for i := 0; i < readers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = rep.Read(context.Background(), "k")
			}()
		}
		nv := be.set("k", "new")
		_ = rep.ApplyNotification(bus.Notification{Key: "k", Version: nv})
		close(gate)
		wg.Wait()

		if info := rep.Inspect("k"); info.State == entry.Valid && info.Version.Before(nv) {
			t.Fatalf("trial %d: cached stale version %v (known %v)", trial, info.Version, nv)
		}
	}
}
