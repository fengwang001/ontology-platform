package redolog

import (
	"errors"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

func mustBuffer(t *testing.T, l0, b, cap, mx int64) *Buffer {
	t.Helper()
	buf, err := NewBuffer(l0, b, cap, mx)
	if err != nil {
		t.Fatalf("NewBuffer(%d,%d,%d,%d) failed: %v", l0, b, cap, mx, err)
	}
	return buf
}

func mustReserve(t *testing.T, buf *Buffer, n int64) (int64, int64) {
	t.Helper()
	start, end, err := buf.Reserve(n)
	if err != nil {
		t.Fatalf("Reserve(%d) failed: %v", n, err)
	}
	return start, end
}

func mustComplete(t *testing.T, buf *Buffer, start int64) {
	t.Helper()
	if err := buf.Complete(start); err != nil {
		t.Fatalf("Complete(%d) failed: %v", start, err)
	}
}

func checkFlush(t *testing.T, buf *Buffer, force bool, wantBlocks, wantFd int64, wantWoken []int64) {
	t.Helper()
	blocks, fd, woken := buf.Flush(force)
	if blocks != wantBlocks || fd != wantFd || !reflect.DeepEqual(woken, wantWoken) {
		t.Fatalf("Flush(%v) = (%d, %d, %v), want (%d, %d, %v)",
			force, blocks, fd, woken, wantBlocks, wantFd, wantWoken)
	}
	if got := buf.Flushed(); got != wantFd {
		t.Fatalf("Flushed() = %d, want %d", got, wantFd)
	}
}

func TestNewBufferValidation(t *testing.T) {
	valid := []struct{ l0, b, cap, mx int64 }{
		{0, 1, 1, 1},
		{1_000_000_000_000, 65_536, 1_000_000_000, 1_000_000},
		{123, 512, 2048, 2},
	}
	for _, p := range valid {
		if _, err := NewBuffer(p.l0, p.b, p.cap, p.mx); err != nil {
			t.Fatalf("NewBuffer(%+v) rejected valid params: %v", p, err)
		}
	}
	invalid := []struct{ l0, b, cap, mx int64 }{
		{-1, 512, 2048, 2},
		{1_000_000_000_001, 512, 2048, 2},
		{0, 0, 2048, 2},
		{0, 65_537, 2048, 2},
		{0, 512, 0, 2},
		{0, 512, 1_000_000_001, 2},
		{0, 512, 2048, 0},
		{0, 512, 2048, 1_000_001},
	}
	for _, p := range invalid {
		if _, err := NewBuffer(p.l0, p.b, p.cap, p.mx); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("NewBuffer(%+v) = %v, want ErrInvalidParam", p, err)
		}
	}
}

// TestWorkedExampleBasic replays the first example from the specification.
func TestWorkedExampleBasic(t *testing.T) {
	buf := mustBuffer(t, 0, 512, 2048, 2)

	type iv struct{ start, end int64 }
	want := []iv{{0, 100}, {100, 600}, {600, 630}}
	for i, n := range []int64{100, 500, 30} {
		start, end := mustReserve(t, buf, n)
		if start != want[i].start || end != want[i].end {
			t.Fatalf("Reserve(%d) = [%d,%d), want [%d,%d)", n, start, end, want[i].start, want[i].end)
		}
	}

	// Later intervals complete first: Ready must not advance.
	mustComplete(t, buf, 100)
	mustComplete(t, buf, 600)
	if got := buf.Ready(); got != 0 {
		t.Fatalf("Ready = %d, want 0 (earliest interval incomplete)", got)
	}
	// Completing the earliest interval skips over the whole completed run.
	mustComplete(t, buf, 0)
	if got := buf.Ready(); got != 630 {
		t.Fatalf("Ready = %d, want 630", got)
	}

	for i, end := range []int64{600, 630, 100} {
		sat, err := buf.Wait(int64(i+1), end)
		if err != nil || sat {
			t.Fatalf("Wait(w%d,%d) = (%v,%v), want registered", i+1, end, sat, err)
		}
	}

	// Non-forced flush rounds down to the block boundary.
	checkFlush(t, buf, false, 1, 512, []int64{3})
	// Forced flush writes the partial tail block and counts it.
	checkFlush(t, buf, true, 1, 630, []int64{1, 2})
	// Nothing new is ready below Fd: no-op.
	checkFlush(t, buf, false, 0, 630, []int64{})

	if got := buf.TotalBlocks(); got != 2 {
		t.Fatalf("TotalBlocks = %d, want 2", got)
	}

	// Buffer space freed by the flushes allows the previously rejected reserve.
	if _, _, err := buf.Reserve(1500); err != nil {
		t.Fatalf("Reserve(1500) after flush failed: %v", err)
	}
}

// TestWorkedExampleTruncation replays the Mx truncation example.
func TestWorkedExampleTruncation(t *testing.T) {
	buf := mustBuffer(t, 0, 512, 2048, 2)
	mustReserve(t, buf, 2000)
	mustComplete(t, buf, 0)
	if got := buf.Ready(); got != 2000 {
		t.Fatalf("Ready = %d, want 2000", got)
	}
	// 4 blocks needed but Mx=2: truncate to a block boundary, no partial block.
	checkFlush(t, buf, true, 2, 1024, []int64{})
	checkFlush(t, buf, true, 2, 2000, []int64{})
	if got := buf.TotalBlocks(); got != 4 {
		t.Fatalf("TotalBlocks = %d, want 4", got)
	}
}

func TestReadyPrefixEmptyWithoutIntervals(t *testing.T) {
	buf := mustBuffer(t, 4096, 512, 2048, 4)
	if got := buf.Ready(); got != 4096 {
		t.Fatalf("Ready = %d, want L0=4096 with no intervals", got)
	}
	checkFlush(t, buf, true, 0, 4096, []int64{})
}

func TestNonForcedFlushBelowOneBlockWritesNothing(t *testing.T) {
	buf := mustBuffer(t, 0, 512, 2048, 4)
	mustReserve(t, buf, 100)
	mustComplete(t, buf, 0)
	if got := buf.Ready(); got != 100 {
		t.Fatalf("Ready = %d, want 100", got)
	}
	// floor(100/512)*512 = 0 <= Fd: nothing is written.
	checkFlush(t, buf, false, 0, 0, []int64{})
	// Forced flush writes the single partial block.
	checkFlush(t, buf, true, 1, 100, []int64{})
}

func TestPartialBlockRecountedOnNextFlush(t *testing.T) {
	buf := mustBuffer(t, 0, 512, 2048, 8)
	mustReserve(t, buf, 630)
	mustComplete(t, buf, 0)
	// ceil(630/512)-floor(0/512) = 2: block 1 written partially.
	checkFlush(t, buf, true, 2, 630, []int64{})

	start, _ := mustReserve(t, buf, 700)
	mustComplete(t, buf, start)
	// ceil(1330/512)-floor(630/512) = 3-1 = 2: block 1 counted again.
	checkFlush(t, buf, true, 2, 1330, []int64{})
	if got := buf.TotalBlocks(); got != 4 {
		t.Fatalf("TotalBlocks = %d, want 4", got)
	}
}

func TestTruncationWakesByTruncatedFdOnly(t *testing.T) {
	buf := mustBuffer(t, 0, 512, 4096, 2)
	for _, n := range []int64{1000, 500, 500} {
		start, _ := mustReserve(t, buf, n)
		mustComplete(t, buf, start)
	}
	// Waiters below and above the truncated frontier.
	for i, end := range []int64{1000, 1500, 2000} {
		if sat, err := buf.Wait(int64(i+1), end); err != nil || sat {
			t.Fatalf("Wait(%d,%d) = (%v,%v), want registered", i+1, end, sat, err)
		}
	}
	// force is also truncated to (0+2)*512 = 1024; only end<=1024 wakes.
	checkFlush(t, buf, true, 2, 1024, []int64{1})
	checkFlush(t, buf, true, 2, 2000, []int64{2, 3})
}

func TestMaxBlocksOneAdvancesBlockByBlock(t *testing.T) {
	buf := mustBuffer(t, 0, 100, 1000, 1)
	mustReserve(t, buf, 350)
	mustComplete(t, buf, 0)
	checkFlush(t, buf, true, 1, 100, []int64{})
	checkFlush(t, buf, true, 1, 200, []int64{})
	checkFlush(t, buf, true, 1, 300, []int64{})
	// Only a partial block remains; still one block.
	checkFlush(t, buf, true, 1, 350, []int64{})
	if got := buf.TotalBlocks(); got != 4 {
		t.Fatalf("TotalBlocks = %d, want 4", got)
	}
}

func TestFlushTargetEqualsFdIsNoop(t *testing.T) {
	buf := mustBuffer(t, 0, 512, 2048, 4)
	mustReserve(t, buf, 512)
	mustComplete(t, buf, 0)
	checkFlush(t, buf, false, 1, 512, []int64{})
	// Ready == Fd now: both modes are no-ops.
	checkFlush(t, buf, false, 0, 512, []int64{})
	checkFlush(t, buf, true, 0, 512, []int64{})
	if got := buf.TotalBlocks(); got != 1 {
		t.Fatalf("TotalBlocks = %d, want 1", got)
	}
}

func TestNonAlignedL0BlockCounting(t *testing.T) {
	buf := mustBuffer(t, 100, 512, 4096, 8)
	start, end := mustReserve(t, buf, 200)
	if start != 100 || end != 300 {
		t.Fatalf("Reserve(200) = [%d,%d), want [100,300)", start, end)
	}
	mustComplete(t, buf, start)
	// ceil(300/512)-floor(100/512) = 1-0 = 1.
	checkFlush(t, buf, true, 1, 300, []int64{})

	start, end = mustReserve(t, buf, 300)
	if start != 300 || end != 600 {
		t.Fatalf("Reserve(300) = [%d,%d), want [300,600)", start, end)
	}
	mustComplete(t, buf, start)
	// ceil(600/512)-floor(300/512) = 2-0 = 2.
	checkFlush(t, buf, true, 2, 600, []int64{})
	if got := buf.TotalBlocks(); got != 3 {
		t.Fatalf("TotalBlocks = %d, want 3", got)
	}
}

func TestWaitEndEqualsFdIsSatisfied(t *testing.T) {
	buf := mustBuffer(t, 0, 512, 2048, 4)
	mustReserve(t, buf, 512)
	mustComplete(t, buf, 0)
	checkFlush(t, buf, false, 1, 512, []int64{})

	sat, err := buf.Wait(7, 512)
	if err != nil || !sat {
		t.Fatalf("Wait(7,512) = (%v,%v), want satisfied", sat, err)
	}
	// Not registered: a later flush must not wake it.
	start, _ := mustReserve(t, buf, 100)
	mustComplete(t, buf, start)
	checkFlush(t, buf, true, 1, 612, []int64{})
}

func TestWaitAlreadyFlushedEndNotRegistered(t *testing.T) {
	buf := mustBuffer(t, 0, 512, 2048, 4)
	mustReserve(t, buf, 300)
	mustComplete(t, buf, 0)
	checkFlush(t, buf, true, 1, 300, []int64{})

	sat, err := buf.Wait(1, 300)
	if err != nil || !sat {
		t.Fatalf("Wait(1,300) = (%v,%v), want satisfied", sat, err)
	}
	// Same w may register again for a different, not-yet-flushed end.
	start, end := mustReserve(t, buf, 200)
	mustComplete(t, buf, start)
	if sat, err := buf.Wait(1, end); err != nil || sat {
		t.Fatalf("Wait(1,%d) = (%v,%v), want registered", end, sat, err)
	}
	checkFlush(t, buf, true, 1, 500, []int64{1})
}

func TestWaiterWakeOrderSameEndByRegistration(t *testing.T) {
	buf := mustBuffer(t, 0, 512, 2048, 4)
	mustReserve(t, buf, 100)
	mustComplete(t, buf, 0)
	// Registration order 5, 2, 9 for the same end.
	for _, w := range []int64{5, 2, 9} {
		if sat, err := buf.Wait(w, 100); err != nil || sat {
			t.Fatalf("Wait(%d,100) = (%v,%v), want registered", w, sat, err)
		}
	}
	checkFlush(t, buf, true, 1, 100, []int64{5, 2, 9})
}

func TestWaiterWakeOrderSmallerEndFirst(t *testing.T) {
	buf := mustBuffer(t, 0, 512, 2048, 4)
	s1, _ := mustReserve(t, buf, 100)
	_, e2 := mustReserve(t, buf, 500)
	mustComplete(t, buf, s1)
	mustComplete(t, buf, 100)
	// w1 registered first but has the larger end.
	if sat, err := buf.Wait(1, e2); err != nil || sat {
		t.Fatalf("Wait(1,%d) = (%v,%v), want registered", e2, sat, err)
	}
	if sat, err := buf.Wait(2, 100); err != nil || sat {
		t.Fatalf("Wait(2,100) = (%v,%v), want registered", sat, err)
	}
	checkFlush(t, buf, true, 2, 600, []int64{2, 1})
}

func TestWaiterWokenAtMostOnce(t *testing.T) {
	buf := mustBuffer(t, 0, 512, 2048, 4)
	mustReserve(t, buf, 100)
	mustComplete(t, buf, 0)
	if sat, err := buf.Wait(3, 100); err != nil || sat {
		t.Fatalf("Wait failed: %v %v", sat, err)
	}
	checkFlush(t, buf, true, 1, 100, []int64{3})
	// Registering the same w again is fine after the wake.
	start, end := mustReserve(t, buf, 50)
	mustComplete(t, buf, start)
	if sat, err := buf.Wait(3, end); err != nil || sat {
		t.Fatalf("re-Wait failed: %v %v", sat, err)
	}
	checkFlush(t, buf, true, 1, 150, []int64{3})
}

func TestWaitDuplicateRejected(t *testing.T) {
	buf := mustBuffer(t, 0, 512, 2048, 4)
	mustReserve(t, buf, 100)
	mustComplete(t, buf, 0)
	if sat, err := buf.Wait(1, 100); err != nil || sat {
		t.Fatalf("Wait failed: %v %v", sat, err)
	}
	if _, err := buf.Wait(1, 100); !errors.Is(err, ErrWaiterExists) {
		t.Fatalf("duplicate Wait = %v, want ErrWaiterExists", err)
	}
}

func TestWaitErrors(t *testing.T) {
	buf := mustBuffer(t, 0, 512, 2048, 4)
	if _, err := buf.Wait(-1, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Wait(-1,0) = %v, want ErrInvalidParam", err)
	}
	if _, err := buf.Wait(1, 12345); !errors.Is(err, ErrEndNotFound) {
		t.Fatalf("Wait(1,12345) = %v, want ErrEndNotFound", err)
	}
}

func TestReserveCapacityBoundary(t *testing.T) {
	buf := mustBuffer(t, 0, 512, 2048, 4)
	// Exactly R+n-Fd == Cap is allowed.
	if _, _, err := buf.Reserve(2048); err != nil {
		t.Fatalf("Reserve(2048) with Cap=2048 failed: %v", err)
	}
	// One more byte exceeds the capacity.
	if _, _, err := buf.Reserve(1); !errors.Is(err, ErrBufferFull) {
		t.Fatalf("Reserve(1) = %v, want ErrBufferFull", err)
	}
	if got := buf.Reserved(); got != 2048 {
		t.Fatalf("R = %d, want 2048 (rejected reserve must not move R)", got)
	}
}

func TestReserveInvalidN(t *testing.T) {
	buf := mustBuffer(t, 0, 512, 2048, 4)
	for _, n := range []int64{0, -5, 2049, 1 << 40} {
		if _, _, err := buf.Reserve(n); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("Reserve(%d) = %v, want ErrInvalidParam", n, err)
		}
	}
	if got := buf.Reserved(); got != 0 {
		t.Fatalf("R = %d, want 0 after rejected reserves", got)
	}
}

func TestFlushFreesSpaceForReserve(t *testing.T) {
	buf := mustBuffer(t, 0, 512, 2048, 8)
	mustReserve(t, buf, 630)
	if _, _, err := buf.Reserve(1500); !errors.Is(err, ErrBufferFull) {
		t.Fatalf("Reserve(1500) = %v, want ErrBufferFull (630+1500 > 2048)", err)
	}
	mustComplete(t, buf, 0)
	checkFlush(t, buf, true, 2, 630, []int64{})
	// 630+1500-630 = 1500 <= 2048 now.
	if _, _, err := buf.Reserve(1500); err != nil {
		t.Fatalf("Reserve(1500) after flush failed: %v", err)
	}
}

func TestCompleteErrors(t *testing.T) {
	buf := mustBuffer(t, 0, 512, 2048, 4)
	if err := buf.Complete(999); !errors.Is(err, ErrIntervalNotFound) {
		t.Fatalf("Complete(999) = %v, want ErrIntervalNotFound", err)
	}
	mustReserve(t, buf, 100)
	mustComplete(t, buf, 0)
	if err := buf.Complete(0); !errors.Is(err, ErrIntervalCompleted) {
		t.Fatalf("re-Complete(0) = %v, want ErrIntervalCompleted", err)
	}
	if got := buf.Ready(); got != 100 {
		t.Fatalf("Ready = %d, want 100 (rejections must not change state)", got)
	}
}

func TestRejectedOpsDoNotChangeState(t *testing.T) {
	buf := mustBuffer(t, 0, 512, 2048, 4)
	mustReserve(t, buf, 100)
	if sat, err := buf.Wait(1, 100); err != nil || sat {
		t.Fatalf("Wait failed: %v %v", sat, err)
	}
	beforeR := buf.Reserved()

	buf.Reserve(0)          // invalid n
	buf.Reserve(5000)       // invalid n (> Cap)
	buf.Reserve(2048)       // buffer full
	buf.Complete(777)       // unknown interval
	buf.Wait(-2, 100)       // invalid w
	buf.Wait(2, 555)        // unknown end
	buf.Wait(1, 100)        // duplicate waiter
	mustComplete(t, buf, 0) // valid, moves Ready
	buf.Complete(0)         // duplicate complete

	r, fd, ready, total := buf.Reserved(), buf.Flushed(), buf.Ready(), buf.TotalBlocks()
	if r != beforeR || fd != 0 || ready != 100 || total != 0 {
		t.Fatalf("state changed by rejected ops: R=%d Fd=%d Ready=%d total=%d", r, fd, ready, total)
	}
	// The single registered waiter is still there and wakes once.
	checkFlush(t, buf, true, 1, 100, []int64{1})
}

func TestScannedBound(t *testing.T) {
	buf := mustBuffer(t, 0, 512, 1<<20, 4)
	var completes int64
	for i := 0; i < 100; i++ {
		start, _ := mustReserve(t, buf, 10)
		if i%3 != 0 {
			mustComplete(t, buf, start)
			completes++
		}
	}
	for i := 0; i < 100; i += 3 {
		mustComplete(t, buf, int64(10*i))
		completes++
	}
	if buf.scanned > int64(len(buf.intervals))+completes {
		t.Fatalf("scanned=%d exceeds intervals+completes=%d",
			buf.scanned, int64(len(buf.intervals))+completes)
	}
	if got := buf.Ready(); got != 1000 {
		t.Fatalf("Ready = %d, want 1000", got)
	}
}

// TestConcurrent hammers one buffer from many goroutines. Correctness of the
// interleaving itself is checked by the race detector; here we assert the
// invariants that must hold for any serial-equivalent execution.
func TestConcurrent(t *testing.T) {
	const (
		l0      = int64(0)
		blk     = int64(64)
		cap     = int64(4096)
		mx      = int64(4)
		workers = 8
		opsPer  = 500
	)
	buf := mustBuffer(t, l0, blk, cap, mx)

	var mu sync.Mutex
	wokenSeen := make(map[int64]bool)
	waiterEnd := make(map[int64]int64)
	var flushBlocksSum int64

	var wg sync.WaitGroup
	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)*7919 + 13))
			var myStarts []int64
			var myEnds []int64
			for i := 0; i < opsPer; i++ {
				switch rng.Intn(4) {
				case 0:
					start, end, err := buf.Reserve(1 + rng.Int63n(50))
					if err == nil {
						myStarts = append(myStarts, start)
						myEnds = append(myEnds, end)
					}
				case 1:
					if len(myStarts) > 0 {
						buf.Complete(myStarts[rng.Intn(len(myStarts))])
					}
				case 2:
					if len(myEnds) > 0 {
						w := int64(g)*1_000_000 + int64(i)
						end := myEnds[rng.Intn(len(myEnds))]
						sat, err := buf.Wait(w, end)
						if err == nil && !sat {
							mu.Lock()
							waiterEnd[w] = end
							mu.Unlock()
						}
					}
				case 3:
					blocks, fd, woken := buf.Flush(rng.Intn(2) == 0)
					mu.Lock()
					flushBlocksSum += blocks
					for _, w := range woken {
						if wokenSeen[w] {
							t.Errorf("waiter %d woken twice", w)
						}
						wokenSeen[w] = true
						if end, ok := waiterEnd[w]; ok && end > fd {
							t.Errorf("waiter %d woken with end=%d > Fd=%d", w, end, fd)
						}
					}
					mu.Unlock()
				}
			}
		}(g)
	}
	wg.Wait()

	r, fd, ready, total := buf.Reserved(), buf.Flushed(), buf.Ready(), buf.TotalBlocks()
	if !(l0 <= fd && fd <= ready && ready <= r) {
		t.Fatalf("invariant L0<=Fd<=Ready<=R violated: Fd=%d Ready=%d R=%d", fd, ready, r)
	}
	if r-fd > cap {
		t.Fatalf("invariant R-Fd<=Cap violated: R-Fd=%d Cap=%d", r-fd, cap)
	}
	if total != flushBlocksSum {
		t.Fatalf("TotalBlocks=%d != sum of flush blocks=%d", total, flushBlocksSum)
	}
}
