package api

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

// Internal package api: tests may read frame state through pool.Snapshot
// (read-only) but still cannot see pool's unexported frame-examination
// counter, which lives in package pool and is read only by pool's own tests.

// TestSelfCheck: built-in stream verifies all four invariants against an
// independent naive model implemented inside SelfCheck.
func TestSelfCheck(t *testing.T) {
	if err := New(3).SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestSentinelErrors: failures are decidable via errors.Is, pairwise distinct,
// leave writes unchanged, and the pool stays usable afterwards.
func TestSentinelErrors(t *testing.T) {
	tests := []struct {
		name string
		run  func(*API) error
		want error
	}{
		{"double-unpin", func(a *API) error { return a.Unpin(0) }, ErrUnpinNotPinned},
		{"bad-frame-unpin", func(a *API) error { return a.Unpin(9) }, ErrInvalidFrame},
		{"bad-frame-dirty", func(a *API) error { return a.MarkDirty(-1) }, ErrInvalidFrame},
		{"full-all-pinned", func(a *API) error { _, e := a.Pin(2); return e }, ErrNoEvictableFrame},
	}
	for _, tc := range tests {
		a := New(1)
		if _, err := a.Pin(1); err != nil {
			t.Fatal(err)
		}
		if tc.name == "double-unpin" {
			if err := a.Unpin(0); err != nil {
				t.Fatal(err)
			}
		}
		before := a.Writes()
		if err := tc.run(a); !errors.Is(err, tc.want) {
			t.Fatalf("%s: want %v, got %v", tc.name, tc.want, err)
		}
		if a.Writes() != before {
			t.Fatalf("%s: writes changed after rejection", tc.name)
		}
		if _, err := a.Pin(1); err != nil { // still usable
			t.Fatalf("%s: pool unusable after rejection: %v", tc.name, err)
		}
	}
	if ErrUnpinNotPinned == ErrInvalidFrame || ErrInvalidFrame == ErrNoEvictableFrame ||
		ErrUnpinNotPinned == ErrNoEvictableFrame {
		t.Fatal("sentinel errors must be pairwise distinct")
	}
}

func residentCount(a *API) int {
	n := 0
	for _, f := range a.p.Snapshot() { // strictly read-only
		if f.PageID != -1 {
			n++
		}
	}
	return n
}

// TestConcurrentPins: N goroutines pin distinct pages into an N-frame pool.
// Afterwards every page is resident exactly once and writes stay 0; a
// concurrent read-only observer never sees the resident count decrease.
func TestConcurrentPins(t *testing.T) {
	for _, n := range []int{2, 16, 64} {
		a := New(n)
		stop := make(chan struct{})
		var mono atomic.Int32
		mono.Store(1)
		var owg sync.WaitGroup

		owg.Add(1)
		go func() { // read-only observer: no sleep, yields between reads
			defer owg.Done()
			prev := 0
			for {
				select {
				case <-stop:
					return
				default:
					if cnt := residentCount(a); cnt < prev {
						mono.Store(0)
					} else {
						prev = cnt
					}
					runtime.Gosched()
				}
			}
		}()

		var pwg sync.WaitGroup
		pwg.Add(n)
		for i := 0; i < n; i++ {
			go func(id int) {
				defer pwg.Done()
				if _, err := a.Pin(id); err != nil {
					mono.Store(0)
				}
			}(i)
		}
		pwg.Wait()
		close(stop)
		owg.Wait()

		if mono.Load() != 1 {
			t.Fatalf("n=%d: resident count observed to decrease", n)
		}
		if a.Writes() != 0 {
			t.Fatalf("n=%d: writes=%d, want 0", n, a.Writes())
		}
		snap := a.p.Snapshot()
		seen := map[int]bool{}
		for _, f := range snap {
			if f.PageID == -1 || seen[f.PageID] {
				t.Fatalf("n=%d: missing or duplicate residency: %+v", n, f)
			}
			seen[f.PageID] = true
		}
		if len(seen) != n {
			t.Fatalf("n=%d: resident=%d, want %d", n, len(seen), n)
		}
	}
}
