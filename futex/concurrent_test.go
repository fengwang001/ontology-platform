package futex

import (
	"runtime"
	"sync"
	"testing"
)

// TestConcurrentWaitStoreWake runs 10000 Wait calls concurrently with a
// Store+Wake producer. A successful Wait linearizes its value check and its
// enqueue together, so any thread that read the old value is guaranteed to
// be queued before the Wake runs: no wake can be lost.
func TestConcurrentWaitStoreWake(t *testing.T) {
	const N = 10_000
	f := New(N)
	f.Store(42, 0)

	var wg sync.WaitGroup
	start := make(chan struct{})
	blocked := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(N)

	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(tid int) {
			defer wg.Done()
			ready.Done()
			<-start
			_ = f.Wait(tid, 42, 0, 0xFFFFFFFF, tid%100, 0)
		}(i)
	}

	// All waiter goroutines are parked at the barrier before the flip.
	ready.Wait()

	// Flip the word to 1, then wake everything queued at the address.
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		<-blocked
		f.Store(42, 1)
		// After the Store no new waiter can enqueue at 42 (the word is now
		// 1 forever), so drain until the queue is observably empty.
		for len(f.Waiters(42)) > 0 {
			w, err := f.Wake(42, N, 0xFFFFFFFF)
			if err != nil {
				t.Errorf("wake: %v", err)
				return
			}
			if len(w) == 0 {
				t.Errorf("non-empty queue but wake returned nothing")
				return
			}
		}
	}()

	// Release waiters, wait until at least one is queued (they all contend
	// on the same internal lock), then release the producer. This keeps the
	// test non-degenerate when the scheduler runs the producer first.
	close(start)
	for len(f.Waiters(42)) == 0 {
		runtime.Gosched()
	}
	close(blocked)
	wg.Wait()

	woken, changed := 0, 0
	for i := 0; i < N; i++ {
		st, _, _ := f.StateOf(i)
		switch st {
		case StateWoken:
			woken++
		case StateIdle:
			// ErrChanged: the Wait saw the new value and needed no wake.
			changed++
		default:
			t.Fatalf("tid %d in unexpected state %v", i, st)
		}
	}
	if woken+changed != N {
		t.Fatalf("woken=%d changed=%d, sum=%d != %d", woken, changed, woken+changed, N)
	}
	if woken == 0 {
		t.Fatal("no thread observed the old value; test is degenerate")
	}
	if len(f.Waiters(42)) != 0 {
		t.Fatalf("waiters remain at addr (lost wake): %v", f.Waiters(42))
	}
	t.Logf("woken=%d changed=%d: every waiter of the old value was woken, no lost wake", woken, changed)
}
