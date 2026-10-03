package futex

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// TestAdvanceExaminedBound verifies that with 1e5 waiters on distinct
// addresses and deadlines, Advance examines at most timeouts+1 entries.
func TestAdvanceExaminedBound(t *testing.T) {
	const n = 100_000
	f := mustNew(t, n)
	for i := 0; i < n; i++ {
		mustWait(t, f, int64(i), int64(i), 0, 0x1, i%prioLevels, int64(i+1))
	}
	f.ResetExamined()
	got, err := f.Advance(n)
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if len(got) != n {
		t.Fatalf("timed out %d, want %d", len(got), n)
	}
	for i := 1; i < n; i++ {
		if got[i] <= got[i-1] {
			t.Fatalf("timeout order not (deadline, seq) at %d", i)
		}
	}
	if e := f.Examined(); e > int64(len(got))+1 {
		t.Fatalf("examined %d, want <= %d", e, len(got)+1)
	}
	// A second advance on an empty heap examines nothing.
	f.ResetExamined()
	if _, err := f.Advance(n); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if e := f.Examined(); e != 0 {
		t.Fatalf("examined on empty heap: got %d, want 0", e)
	}
}

// TestWakeExaminedBound verifies Wake examines at most n plus the skipped
// mismatches, independent of waiters on other addresses.
func TestWakeExaminedBound(t *testing.T) {
	const addrs = 1000
	const perAddr = 100
	f := mustNew(t, addrs*perAddr)
	for a := 0; a < addrs; a++ {
		for i := 0; i < perAddr; i++ {
			bit := uint32(0x2)
			if i%2 == 0 {
				bit = 0x1
			}
			mustWait(t, f, int64(a*perAddr+i), int64(a), 0, bit, 5, 0)
		}
	}
	f.ResetExamined()
	got, err := f.Wake(500, 3, 0x1)
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("woke %d, want 3", len(got))
	}
	// 3 woken + at most 1 skipped mismatch between matches (waiters
	// alternate bitsets, so pattern is match,skip,match,skip,match).
	if e := f.Examined(); e != 5 {
		t.Fatalf("examined %d, want 5 (3 woken + 2 skipped)", e)
	}
}

// TestConcurrentWaitStoreWakeNoLostWakeup runs 1e4 concurrent Waiters
// against Store+Wake and checks the global invariant: every successful
// Wait is accounted for as woken, timed out, cancelled or still waiting.
func TestConcurrentWaitStoreWakeNoLostWakeup(t *testing.T) {
	const n = 10_000
	f := mustNew(t, n)
	var enqueued atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(tid int64) {
			defer wg.Done()
			err := f.Wait(tid, 42, 0, 0x1, 50, 0)
			switch {
			case err == nil:
				enqueued.Add(1)
			case errors.Is(err, ErrValueChanged):
				// Lost the race against Store: never enqueued.
			default:
				t.Errorf("unexpected Wait error: %v", err)
			}
		}(int64(i))
	}
	// Race with the waiters: flip the word, then wake in batches.
	if err := f.Store(42, 1); err != nil {
		t.Fatalf("Store: %v", err)
	}
	var woken int64
	for {
		got, err := f.Wake(42, 997, 0x1)
		if err != nil {
			t.Fatalf("Wake: %v", err)
		}
		woken += int64(len(got))
		if len(got) == 0 {
			break
		}
	}
	wg.Wait()
	// Drain anyone who enqueued after the last Wake but before its Store
	// check; none should remain because Store already happened, so any
	// successful Wait must have been woken above. Waiters enqueued after
	// the final Wake are impossible: their value check would fail.
	if got := f.Waiters(42); len(got) != 0 {
		t.Fatalf("lost wakeup: %d waiters left after Store+Wake storm", len(got))
	}
	if enqueued.Load() != woken {
		t.Fatalf("enqueued %d != woken %d", enqueued.Load(), woken)
	}
	// Global invariant: successful waits == woken+timedout+cancelled+waiting.
	var waiting, wokenState int64
	for tid := int64(0); tid < n; tid++ {
		st, _ := f.State(tid)
		switch st {
		case StateWaiting:
			waiting++
		case StateWoken:
			wokenState++
		}
	}
	if enqueued.Load() != waiting+wokenState {
		t.Fatalf("invariant: waits=%d waiting=%d woken=%d", enqueued.Load(), waiting, wokenState)
	}
}

// TestConcurrentMixed hammers the subsystem with mixed operations and
// verifies the structural invariants afterwards.
func TestConcurrentMixed(t *testing.T) {
	f := mustNew(t, 4096)
	var wg sync.WaitGroup
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func(base int64) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				tid := base*500 + int64(i)
				addr := int64(i % 16)
				switch i % 5 {
				case 0:
					_ = f.Wait(tid, addr, 0, 0x1, i%prioLevels, 0)
				case 1:
					_ = f.Store(addr, 0)
				case 2:
					_, _ = f.Wake(addr, 3, 0x1)
				case 3:
					_ = f.Cancel(tid - 1)
				case 4:
					_, _, _ = f.WakeOp(addr, addr, 1, 1, OpAdd, 0, CmpGE, 0)
				}
			}
		}(int64(g))
	}
	wg.Wait()
	f.checkInvariants(t)
}

// checkInvariants verifies the structural invariants of the subsystem.
func (f *Futex) checkInvariants(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	queued := 0
	seen := make(map[int64]int64) // tid -> addr
	for addr, q := range f.queues {
		queued += q.length
		q.forEach(func(w *waiter) bool {
			if w.addr != addr {
				t.Errorf("waiter %d queued on %d but has addr %d", w.tid, addr, w.addr)
			}
			if prev, dup := seen[w.tid]; dup {
				t.Errorf("thread %d in two queues: %d and %d", w.tid, prev, addr)
			}
			seen[w.tid] = addr
			return true
		})
	}
	if queued != f.waiting {
		t.Errorf("queue lengths sum %d != waiting %d", queued, f.waiting)
	}
	if f.waiting > f.cap {
		t.Errorf("waiting %d exceeds capacity %d", f.waiting, f.cap)
	}
	stateWaiting := 0
	tracked := 0
	for tid, rec := range f.threads {
		if rec.state == StateWaiting {
			stateWaiting++
			if _, ok := seen[tid]; !ok {
				t.Errorf("thread %d waiting but not queued", tid)
			}
			if rec.w != nil && rec.w.deadline != 0 {
				tracked++
			}
		}
	}
	if stateWaiting != f.waiting {
		t.Errorf("threads in waiting state %d != waiting %d", stateWaiting, f.waiting)
	}
	if len(f.deadlines) != tracked {
		t.Errorf("timeout heap size %d != waiters with deadline %d", len(f.deadlines), tracked)
	}
}
