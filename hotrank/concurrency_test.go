package hotrank

import (
	"math/rand"
	"sync"
	"testing"
)

// Add must not scan all W buckets: each accepted Add touches at most the
// single target bucket plus buckets actually leaving the window. Verified for
// W=10 and W=1000; the bound is independent of W.
func TestAddDoesNotScanWindow(t *testing.T) {
	for _, w := range []int64{10, 1000} {
		b, _ := New(10, w, 1, 100, 1)
		if err := b.Add("a", 5, 5); err != nil { // establish c=5, cur=0
			t.Fatal(err)
		}
		start := b.addExpireScans
		// Late, non-advancing adds inside the window never expire anything.
		for i := 0; i < 500; i++ {
			if err := b.Add("a", 1, 5); err != nil {
				t.Fatalf("W=%d late add %d: %v", w, i, err)
			}
		}
		if got := b.addExpireScans - start; got != 0 {
			t.Fatalf("W=%d scans for in-window adds = %d", w, got)
		}

		// A big jump evicts one bucket, costing exactly one (bucket,id)
		// examination regardless of W.
		start = b.addExpireScans
		if err := b.Add("a", 1, int64(w+2)*10); err != nil {
			t.Fatal(err)
		}
		got := b.addExpireScans - start
		if got != 1 {
			t.Fatalf("W=%d eviction scans = %d, want 1 (independent of W)", w, got)
		}
	}
}

// Concurrent calls must be safe and equivalent to some serial order, with all
// board invariants holding on every observed snapshot.
func TestConcurrent(t *testing.T) {
	b, _ := New(7, 5, 10, 8, 2)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	var clockMu sync.Mutex
	clock := int64(1000)
	var errMu sync.Mutex
	var unexpectedErr error
	snapshot := func() {
		clockMu.Lock()
		clock += 3
		now := clock
		clockMu.Unlock()
		r, err := b.Snapshot(now)
		if err == ErrClockRewind {
			return
		}
		if err != nil {
			errMu.Lock()
			unexpectedErr = err
			errMu.Unlock()
			return
		}
		assertInvariants(t, r, 8)
	}
	now := func() int64 {
		clockMu.Lock()
		defer clockMu.Unlock()
		return clock
	}

	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				tm := int64((g*7 + i*2) % 1000) // always below snapshot times
				_ = b.Add("id"+string(rune('a'+i%6)), int64(1+i%20), tm)
			}
		}(g)
	}
	for g := 0; g < 3; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				snapshot()
				_ = b.Score("ida")
			}
		}()
	}

	// Reader-only Peeks with an advancing time argument.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 300; i++ {
			_, _ = b.Peek(now())
		}
	}()

	close(stop)
	wg.Wait()
	if unexpectedErr != nil {
		t.Fatal(unexpectedErr)
	}
}

func assertInvariants(t *testing.T, r Result, k int64) {
	t.Helper()
	if int64(len(r.Board)) > k {
		t.Fatalf("board exceeds K: %d", len(r.Board))
	}
	for i := 1; i < len(r.Board); i++ {
		prev, cur := r.Board[i-1], r.Board[i]
		if prev.Score < cur.Score || (prev.Score == cur.Score && prev.ID >= cur.ID) {
			t.Fatalf("board order violated: %+v after %+v", cur, prev)
		}
		if prev.Score == cur.Score && prev.Rank != cur.Rank {
			t.Fatalf("tie ranks differ: %+v %+v", prev, cur)
		}
	}
}

// Same operation sequence replayed twice gives identical observable output.
func TestReplayDeterminism(t *testing.T) {
	cfg := genConfig{l: 5, w: 4, m: 20, k: 3, hs: 2,
		ids: []string{"x", "y", "z"}, tmax: 120, badRate: 10}
	rng := rand.New(rand.NewSource(42))
	ops := make([]op, 100)
	for i := range ops {
		ops[i] = genOp(rng, cfg)
	}
	fp1 := replayFingerprint(cfg, ops)
	fp2 := replayFingerprint(cfg, ops)
	if fp1 != fp2 {
		t.Fatal("replay differs")
	}
}
