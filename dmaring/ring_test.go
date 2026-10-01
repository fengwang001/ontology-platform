package dmaring

import (
	"errors"
	"testing"
)

func mustRing(t *testing.T, n int) *Ring {
	t.Helper()
	r, err := NewRing(n)
	if err != nil {
		t.Fatalf("NewRing(%d) unexpected error: %v", n, err)
	}
	return r
}

func mustSubmit(t *testing.T, r *Ring, lens ...int) {
	t.Helper()
	if err := r.Submit(lens); err != nil {
		t.Fatalf("Submit(%v) unexpected error: %v", lens, err)
	}
}

func mustReap(t *testing.T, r *Ring) ReapResult {
	t.Helper()
	res, err := r.Reap()
	if err != nil {
		t.Fatalf("Reap() unexpected error: %v", err)
	}
	return res
}

func checkPtrs(t *testing.T, r *Ring, prod, dev, reap uint64) {
	t.Helper()
	p, d, rp := r.Pointers()
	if p != prod || d != dev || rp != reap {
		t.Fatalf("Pointers() = (%d,%d,%d), want (%d,%d,%d)", p, d, rp, prod, dev, reap)
	}
}

func checkFree(t *testing.T, r *Ring, want int) {
	t.Helper()
	if got := r.Free(); got != want {
		t.Fatalf("Free() = %d, want %d", got, want)
	}
}

func TestNewRingValidation(t *testing.T) {
	for _, n := range []int{-2, 0, 1, 3, 5, 6, 7, 12} {
		if _, err := NewRing(n); !errors.Is(err, ErrInvalidSlotCount) {
			t.Errorf("NewRing(%d): want ErrInvalidSlotCount, got %v", n, err)
		}
	}
	for _, n := range []int{2, 4, 8, 1024} {
		if _, err := NewRing(n); err != nil {
			t.Errorf("NewRing(%d): want nil, got %v", n, err)
		}
	}
}

func TestSubmitValidationOrder(t *testing.T) {
	r := mustRing(t, 4)
	// Empty list is reported before anything else.
	if err := r.Submit(nil); !errors.Is(err, ErrEmptyLens) {
		t.Errorf("Submit(nil): want ErrEmptyLens, got %v", err)
	}
	// Bad length is reported before the count checks, even when the
	// list is also longer than N.
	if err := r.Submit([]int{1, 0, 1, 1, 1, 1}); !errors.Is(err, ErrNonPositiveLen) {
		t.Errorf("Submit with zero len: want ErrNonPositiveLen, got %v", err)
	}
	if err := r.Submit([]int{-3}); !errors.Is(err, ErrNonPositiveLen) {
		t.Errorf("Submit with negative len: want ErrNonPositiveLen, got %v", err)
	}
	// Count > N is reported before the Free check (Free == N here).
	if err := r.Submit([]int{1, 1, 1, 1, 1}); !errors.Is(err, ErrTooManySegments) {
		t.Errorf("Submit 5 segments into N=4: want ErrTooManySegments, got %v", err)
	}
	// Count <= N but > Free.
	mustSubmit(t, r, 10, 20, 30)
	if err := r.Submit([]int{1, 1}); !errors.Is(err, ErrInsufficientFree) {
		t.Errorf("Submit 2 segments with Free=1: want ErrInsufficientFree, got %v", err)
	}
	// Rejected submits changed nothing.
	checkPtrs(t, r, 3, 0, 0)
	checkFree(t, r, 1)
}

func TestSubmitExactFreeAndOverflow(t *testing.T) {
	r := mustRing(t, 8)
	mustSubmit(t, r, 1, 2, 3) // prod=3, Free=5
	// Exactly Free segments: accepted, ring becomes full (no empty slot kept).
	mustSubmit(t, r, 4, 5, 6, 7, 8)
	checkFree(t, r, 0)
	checkPtrs(t, r, 8, 0, 0)
	// One more than Free (Free=0): rejected.
	if err := r.Submit([]int{9}); !errors.Is(err, ErrInsufficientFree) {
		t.Fatalf("Submit into full ring: want ErrInsufficientFree, got %v", err)
	}
	// Reap one packet (3 segments), then Free+1 segments must fail while
	// exactly Free segments succeed.
	if _, err := r.DeviceRun(3); err != nil {
		t.Fatalf("DeviceRun: %v", err)
	}
	res := mustReap(t, r)
	if res.Segments != 3 || res.TotalLen != 6 || res.ErrIndex != -1 {
		t.Fatalf("Reap() = %+v, want {3 6 -1}", res)
	}
	checkFree(t, r, 3)
	if err := r.Submit([]int{1, 1, 1, 1}); !errors.Is(err, ErrInsufficientFree) {
		t.Fatalf("Submit Free+1 segments: want ErrInsufficientFree, got %v", err)
	}
	mustSubmit(t, r, 1, 1, 1)
	checkFree(t, r, 0)
}

func TestSubmitEqualNAndGreaterThanN(t *testing.T) {
	r := mustRing(t, 4)
	// m == N fills the whole ring and is legal.
	mustSubmit(t, r, 7, 8, 9, 10)
	checkFree(t, r, 0)
	s := r.slots[0]
	if !s.First || s.Last || s.Len != 7 {
		t.Errorf("slot0 = %+v, want First only, Len 7", s)
	}
	if s := r.slots[3]; s.First || !s.Last || s.Len != 10 {
		t.Errorf("slot3 = %+v, want Last only, Len 10", s)
	}
	// m > N is rejected as ErrTooManySegments regardless of Free.
	if err := r.Submit([]int{1, 1, 1, 1, 1}); !errors.Is(err, ErrTooManySegments) {
		t.Fatalf("Submit 5 into N=4: want ErrTooManySegments, got %v", err)
	}
}

func TestSingleSegmentFirstAndLast(t *testing.T) {
	r := mustRing(t, 4)
	mustSubmit(t, r, 42)
	s := r.slots[0]
	if !s.Own || !s.First || !s.Last || s.Len != 42 || s.St != 0 {
		t.Fatalf("slot0 = %+v, want Own=First=Last=true Len=42 St=0", s)
	}
	if _, err := r.DeviceRun(1); err != nil {
		t.Fatalf("DeviceRun: %v", err)
	}
	res := mustReap(t, r)
	if res.Segments != 1 || res.TotalLen != 42 || res.ErrIndex != -1 {
		t.Fatalf("Reap() = %+v, want {1 42 -1}", res)
	}
	checkPtrs(t, r, 1, 1, 1)
}

func TestFreeUnchangedUntilReap(t *testing.T) {
	r := mustRing(t, 4)
	mustSubmit(t, r, 5, 6)
	mustSubmit(t, r, 7)
	checkFree(t, r, 1)
	// Device processes everything; Free must not move.
	done, err := r.DeviceRun(10)
	if err != nil || done != 3 {
		t.Fatalf("DeviceRun(10) = (%d,%v), want (3,nil)", done, err)
	}
	checkFree(t, r, 1)
	checkPtrs(t, r, 3, 3, 0)
	// Reaping returns slots one packet at a time.
	mustReap(t, r)
	checkFree(t, r, 3)
	mustReap(t, r)
	checkFree(t, r, 4)
	checkPtrs(t, r, 3, 3, 3)
}

func TestDeviceRunStopsOnOwnZeroAndNegativeK(t *testing.T) {
	r := mustRing(t, 4)
	mustSubmit(t, r, 1, 1)
	if _, err := r.DeviceRun(-1); !errors.Is(err, ErrNegativeK) {
		t.Fatalf("DeviceRun(-1): want ErrNegativeK, got %v", err)
	}
	done, _ := r.DeviceRun(2)
	if done != 2 {
		t.Fatalf("DeviceRun(2) = %d, want 2", done)
	}
	// No descriptors owned by the device remain: run is a no-op.
	done, _ = r.DeviceRun(5)
	if done != 0 {
		t.Fatalf("DeviceRun(5) with no owned slots = %d, want 0", done)
	}
	// Reap nothing, submit again: dev must stop at the unsubmitted gap.
	mustReap(t, r)
	mustSubmit(t, r, 9)
	done, _ = r.DeviceRun(4)
	if done != 1 {
		t.Fatalf("DeviceRun(4) = %d, want 1 (stop at OWN=0 slot)", done)
	}
	checkPtrs(t, r, 3, 3, 2)
}

func TestReapErrorOrder(t *testing.T) {
	r := mustRing(t, 4)
	// No packet at all: ErrNoPacket wins.
	if _, err := r.Reap(); !errors.Is(err, ErrNoPacket) {
		t.Fatalf("Reap on empty ring: want ErrNoPacket, got %v", err)
	}
	// Packet present but still owned by the device.
	mustSubmit(t, r, 3, 4)
	if _, err := r.Reap(); !errors.Is(err, ErrPacketIncomplete) {
		t.Fatalf("Reap with OWN=1 segments: want ErrPacketIncomplete, got %v", err)
	}
	// Partially processed packet is still incomplete.
	if _, err := r.DeviceRun(1); err != nil {
		t.Fatalf("DeviceRun: %v", err)
	}
	if _, err := r.Reap(); !errors.Is(err, ErrPacketIncomplete) {
		t.Fatalf("Reap half-done packet: want ErrPacketIncomplete, got %v", err)
	}
	// Rejected reaps changed nothing.
	checkPtrs(t, r, 2, 1, 0)
	checkFree(t, r, 2)
}

func TestWrapAroundSlotIndexing(t *testing.T) {
	r := mustRing(t, 8)
	// Push the pointers past N so slot = ptr % N actually wraps.
	mustSubmit(t, r, 1, 2, 3)    // seqs 0..2
	mustSubmit(t, r, 4, 5, 6, 7) // seqs 3..6
	if _, err := r.DeviceRun(3); err != nil {
		t.Fatalf("DeviceRun: %v", err)
	}
	mustReap(t, r) // frees seqs 0..2
	checkPtrs(t, r, 7, 3, 3)
	// Submit 3 more: seqs 7,8,9 -> slots 3,0,1 wrap around.
	mustSubmit(t, r, 10, 11, 12)
	checkPtrs(t, r, 10, 3, 3)
	// Slot 0 now holds seq 8 (len 11, middle segment), slot 1 holds seq 9.
	if s := r.slots[0]; !s.Own || s.First || s.Last || s.Len != 11 {
		t.Errorf("slot0 = %+v, want wrapped middle segment Len=11", s)
	}
	if s := r.slots[1]; !s.Own || s.First || !s.Last || s.Len != 12 {
		t.Errorf("slot1 = %+v, want wrapped Last segment Len=12", s)
	}
	if s := r.slots[7]; !s.Own || !s.First || s.Last || s.Len != 10 {
		t.Errorf("slot7 = %+v, want First segment Len=10", s)
	}
	// Drain everything and verify reaped lengths follow submission order.
	if _, err := r.DeviceRun(10); err != nil {
		t.Fatalf("DeviceRun: %v", err)
	}
	res := mustReap(t, r)
	if res.Segments != 4 || res.TotalLen != 22 {
		t.Fatalf("Reap() = %+v, want {4 22 -1}", res)
	}
	res = mustReap(t, r)
	if res.Segments != 3 || res.TotalLen != 33 {
		t.Fatalf("Reap() = %+v, want {3 33 -1}", res)
	}
	checkPtrs(t, r, 10, 10, 10)
	checkFree(t, r, 8)
}

func TestFaultCascadeAndReapErrIndex(t *testing.T) {
	r := mustRing(t, 8)
	mustSubmit(t, r, 10, 20, 30, 40) // seqs 0..3
	// Fault on the second segment (unbounded seq 1).
	if err := r.Fault(1); err != nil {
		t.Fatalf("Fault(1): %v", err)
	}
	done, _ := r.DeviceRun(8)
	// seq0 normal (1 unit), cascade at seq1 covers seqs 1..3 (1 unit).
	if done != 2 {
		t.Fatalf("DeviceRun(8) = %d, want 2 units", done)
	}
	checkPtrs(t, r, 4, 4, 0)
	wantSt := []int{1, 2, 3, 3}
	for i, want := range wantSt {
		if got := r.slots[i].St; got != want {
			t.Errorf("slot%d St = %d, want %d", i, got, want)
		}
		if r.slots[i].Own {
			t.Errorf("slot%d still owned by device", i)
		}
	}
	res := mustReap(t, r)
	if res.Segments != 4 || res.TotalLen != 100 || res.ErrIndex != 1 {
		t.Fatalf("Reap() = %+v, want {4 100 1}", res)
	}
}

func TestFaultVoidedByCascade(t *testing.T) {
	r := mustRing(t, 8)
	mustSubmit(t, r, 1, 2, 3, 4) // seqs 0..3, one packet
	mustSubmit(t, r, 5, 6)       // seqs 4..5, next packet
	// Fault on seq 1 (head of cascade) and seq 3 (discarded by cascade).
	mustFault(t, r, 1)
	mustFault(t, r, 3)
	done, _ := r.DeviceRun(8)
	// Units: seq0 ok, cascade seq1..3, seq4 ok, seq5 ok -> 4 units.
	if done != 4 {
		t.Fatalf("DeviceRun(8) = %d, want 4", done)
	}
	// The registration on seq 3 was voided: seq 3 is st=3, not st=2.
	if got := r.slots[3].St; got != 3 {
		t.Fatalf("slot3 St = %d, want 3 (fault registration voided)", got)
	}
	res := mustReap(t, r)
	if res.Segments != 4 || res.ErrIndex != 1 {
		t.Fatalf("Reap() = %+v, want ErrIndex 1", res)
	}
	// Second packet is clean.
	res = mustReap(t, r)
	if res.Segments != 2 || res.TotalLen != 11 || res.ErrIndex != -1 {
		t.Fatalf("Reap() = %+v, want {2 11 -1}", res)
	}
}

func TestCascadeCountsOneUnit(t *testing.T) {
	r := mustRing(t, 8)
	mustSubmit(t, r, 1, 2, 3) // seqs 0..2
	mustSubmit(t, r, 4)       // seq 3
	mustFault(t, r, 0)
	// k=1: the whole cascade (3 descriptors) costs exactly one unit,
	// so seq 3 must remain untouched.
	done, _ := r.DeviceRun(1)
	if done != 1 {
		t.Fatalf("DeviceRun(1) = %d, want 1", done)
	}
	checkPtrs(t, r, 4, 3, 0)
	if s := r.slots[3]; !s.Own || s.St != 0 {
		t.Errorf("slot3 = %+v, want untouched (Own, St=0)", s)
	}
	for i := 0; i < 3; i++ {
		want := 3
		if i == 0 {
			want = 2
		}
		if got := r.slots[i].St; got != want {
			t.Errorf("slot%d St = %d, want %d", i, got, want)
		}
	}
	// The next unit processes seq 3.
	done, _ = r.DeviceRun(1)
	if done != 1 {
		t.Fatalf("DeviceRun(1) = %d, want 1", done)
	}
	checkPtrs(t, r, 4, 4, 0)
}

func TestFaultValidation(t *testing.T) {
	r := mustRing(t, 4)
	mustSubmit(t, r, 1, 1)
	if _, err := r.DeviceRun(1); err != nil {
		t.Fatalf("DeviceRun: %v", err)
	}
	// seq < dev is rejected.
	if err := r.Fault(0); !errors.Is(err, ErrFaultBeforeDev) {
		t.Fatalf("Fault(0) with dev=1: want ErrFaultBeforeDev, got %v", err)
	}
	// seq >= prod is allowed (takes effect after a future submit).
	mustFault(t, r, 3)
	// Duplicate registration has no effect.
	mustFault(t, r, 3)
	mustFault(t, r, 3)
	mustSubmit(t, r, 7) // seq 2
	mustSubmit(t, r, 8) // seq 3
	done, _ := r.DeviceRun(4)
	if done != 3 { // seq1 ok, seq2 ok, cascade at seq3
		t.Fatalf("DeviceRun(4) = %d, want 3", done)
	}
	if got := r.slots[3].St; got != 2 {
		t.Fatalf("slot3 St = %d, want 2", got)
	}
	res := mustReap(t, r) // packet {1,1}
	if res.ErrIndex != -1 {
		t.Fatalf("Reap() ErrIndex = %d, want -1", res.ErrIndex)
	}
	mustReap(t, r) // packet {7}
	res = mustReap(t, r)
	if res.Segments != 1 || res.TotalLen != 8 || res.ErrIndex != 0 {
		t.Fatalf("Reap() = %+v, want {1 8 0}", res)
	}
}

func mustFault(t *testing.T, r *Ring, seq uint64) {
	t.Helper()
	if err := r.Fault(seq); err != nil {
		t.Fatalf("Fault(%d) unexpected error: %v", seq, err)
	}
}
