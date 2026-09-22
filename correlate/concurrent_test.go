package correlate

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrentDeliveryExactlyOneSuccess(t *testing.T) {
	cor, _ := newTestCorrelator(8)
	tok, _ := cor.Issue(time.Minute)

	const n = 64
	var wg sync.WaitGroup
	var success, idle, stale, unknown int64
	start := make(chan struct{})
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			<-start
			err := cor.Deliver(tok)
			switch {
			case err == nil:
				atomic.AddInt64(&success, 1)
			case errors.Is(err, ErrOrphanIdle):
				atomic.AddInt64(&idle, 1)
			case errors.Is(err, ErrOrphanStale):
				atomic.AddInt64(&stale, 1)
			case errors.Is(err, ErrOrphanUnknown):
				atomic.AddInt64(&unknown, 1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if success != 1 {
		t.Fatalf("successes = %d, want exactly 1", success)
	}
	if success+idle+stale+unknown != n {
		t.Fatal("some delivery had an unexpected result")
	}
	if idle+stale+unknown != n-1 {
		t.Fatalf("failures = %d, want %d", idle+stale+unknown, n-1)
	}
	snap := cor.SnapshotState()
	if snap.InFlight != 0 || snap.Completed != 1 || snap.Orphans() != n-1 {
		t.Fatalf("inconsistent counters: %+v", snap)
	}
}

func TestConcurrentMixedOperationsStayConsistent(t *testing.T) {
	cor, _ := newTestCorrelator(16)
	const goroutines = 32
	var wg sync.WaitGroup
	var issued int64
	start := make(chan struct{})

	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range 100 {
				tok, err := cor.Issue(50 * time.Millisecond)
				if err == nil {
					atomic.AddInt64(&issued, 1)
					_ = cor.Deliver(tok)
				}
				_ = cor.SnapshotState()
			}
		}()
	}
	close(start)
	wg.Wait()

	snap := cor.SnapshotState()
	if snap.InFlight < 0 || snap.InFlight > cor.Capacity() {
		t.Fatalf("bad inflight: %d", snap.InFlight)
	}
	// Every delivery increments either Completed or exactly one orphan class.
	if snap.Completed+
		snap.OrphanUnknown+snap.OrphanIdle+snap.OrphanStale !=
		uint64(issued) {
		t.Fatalf("delivery accounting mismatch: %+v", snap)
	}
}
