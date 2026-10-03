package heap

import (
	"errors"
	"slices"
	"testing"
)

func mustAlloc(t *testing.T, h *Heap, s int64, f int, fin bool) int {
	t.Helper()
	id, err := h.Alloc(s, f, fin)
	if err != nil {
		t.Fatalf("Alloc(%d,%d,%v): %v", s, f, fin, err)
	}
	return id
}

func mustRef(t *testing.T, h *Heap, k Kind, target, q int, s, now int64) int {
	t.Helper()
	id, err := h.NewRef(k, target, q, s, now)
	if err != nil {
		t.Fatalf("NewRef(%d,%d,%d,%d,%d): %v", k, target, q, s, now, err)
	}
	return id
}

func mustCollect(t *testing.T, h *Heap, now int64) CollectResult {
	t.Helper()
	res, err := h.Collect(now)
	if err != nil {
		t.Fatalf("Collect(%d): %v", now, err)
	}
	if h.visits != len(h.objects) {
		t.Fatalf("Collect(%d): visit counter %d != survivors %d", now, h.visits, len(h.objects))
	}
	return res
}

func mustGet(t *testing.T, h *Heap, r int, now int64) int {
	t.Helper()
	got, err := h.Get(r, now)
	if err != nil {
		t.Fatalf("Get(%d,%d): %v", r, now, err)
	}
	return got
}

func mustPoll(t *testing.T, h *Heap, q int) int {
	t.Helper()
	got, err := h.Poll(q)
	if err != nil {
		t.Fatalf("Poll(%d): %v", q, err)
	}
	return got
}

func checkInvariants(t *testing.T, h *Heap) {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	var sum int64
	for _, o := range h.objects {
		sum += o.size
	}
	if sum != h.used {
		t.Errorf("used %d != sum of live sizes %d", h.used, sum)
	}
	if h.used > h.capacity {
		t.Errorf("used %d exceeds capacity %d", h.used, h.capacity)
	}
	for _, o := range h.objects {
		if o.ref && o.target != 0 {
			if _, ok := h.objects[o.target]; !ok {
				t.Errorf("ref %d has dangling target %d", o.id, o.target)
			}
		}
	}
}

func eqIDs(a, b []int) bool { return slices.Equal(a, b) }

// TestSpecExample replays the worked example from the specification.
func TestSpecExample(t *testing.T) {
	h, err := New(100, 10, 5)
	if err != nil {
		t.Fatal(err)
	}
	q := h.CreateQueue()
	if q != 1 {
		t.Fatalf("queue = %d, want 1", q)
	}
	o1 := mustAlloc(t, h, 10, 0, false)
	if err := h.SetRoot(o1); err != nil {
		t.Fatal(err)
	}
	o2 := mustAlloc(t, h, 20, 1, false)
	o3 := mustAlloc(t, h, 30, 0, true)
	if err := h.SetField(o2, 0, o3); err != nil {
		t.Fatal(err)
	}
	o4 := mustAlloc(t, h, 10, 0, false)
	w := mustRef(t, h, Weak, o2, q, 5, 0)
	s := mustRef(t, h, Soft, o4, q, 5, 0)
	p := mustRef(t, h, Phantom, o3, q, 5, 0)
	for _, r := range []int{w, s, p} {
		if err := h.SetRoot(r); err != nil {
			t.Fatal(err)
		}
	}
	if got := h.Used(); got != 85 {
		t.Fatalf("used = %d, want 85", got)
	}

	res := mustCollect(t, h, 7)
	if !eqIDs(res.Collected, []int{o2}) {
		t.Errorf("collected = %v, want [%d]", res.Collected, o2)
	}
	if !eqIDs(res.WeakCleared, []int{w}) {
		t.Errorf("weak cleared = %v, want [%d]", res.WeakCleared, w)
	}
	if !eqIDs(res.SoftCleared, nil) || !eqIDs(res.FallbackCleared, nil) {
		t.Errorf("soft=%v fallback=%v, want both empty", res.SoftCleared, res.FallbackCleared)
	}
	if !eqIDs(res.FinalizeSelected, []int{o3}) {
		t.Errorf("finalize selected = %v, want [%d]", res.FinalizeSelected, o3)
	}
	if res.Used != 65 {
		t.Errorf("used = %d, want 65", res.Used)
	}

	if got := mustPoll(t, h, q); got != w {
		t.Errorf("poll = %d, want %d", got, w)
	}
	if got := mustGet(t, h, s, 8); got != o4 {
		t.Errorf("Get(soft) = %d, want %d", got, o4)
	}
	if got := mustGet(t, h, w, 8); got != 0 {
		t.Errorf("Get(weak) = %d, want 0", got)
	}
	if got := mustGet(t, h, p, 8); got != o3 {
		t.Errorf("Get(phantom) = %d, want %d", got, o3)
	}
	fin, err := h.Finalize(1)
	if err != nil || !eqIDs(fin, []int{o3}) {
		t.Errorf("Finalize(1) = %v, %v; want [%d]", fin, err, o3)
	}

	res = mustCollect(t, h, 9)
	if !eqIDs(res.Collected, []int{o3}) {
		t.Errorf("collected = %v, want [%d]", res.Collected, o3)
	}
	if !eqIDs(res.FallbackCleared, []int{p}) {
		t.Errorf("fallback cleared = %v, want [%d]", res.FallbackCleared, p)
	}
	if len(res.FinalizeSelected) != 0 {
		t.Errorf("finalize selected = %v, want empty", res.FinalizeSelected)
	}
	if res.Used != 35 {
		t.Errorf("used = %d, want 35", res.Used)
	}

	res = mustCollect(t, h, 1000)
	if !eqIDs(res.SoftCleared, []int{s}) {
		t.Errorf("soft cleared = %v, want [%d]", res.SoftCleared, s)
	}
	if !eqIDs(res.Collected, []int{o4}) {
		t.Errorf("collected = %v, want [%d]", res.Collected, o4)
	}
	if res.Used != 25 {
		t.Errorf("used = %d, want 25", res.Used)
	}

	// Finalization chain: both objects selected in the same round.
	o8 := mustAlloc(t, h, 1, 0, true)
	o9 := mustAlloc(t, h, 1, 1, true)
	if err := h.SetField(o9, 0, o8); err != nil {
		t.Fatal(err)
	}
	res = mustCollect(t, h, 1000)
	if !eqIDs(res.FinalizeSelected, []int{o8, o9}) {
		t.Errorf("finalize selected = %v, want [%d %d]", res.FinalizeSelected, o8, o9)
	}
	if len(res.Collected) != 0 {
		t.Errorf("collected = %v, want empty (resurrected)", res.Collected)
	}
	// Objects in the finalization queue are roots: a Collect before
	// Finalize must not reclaim them.
	res = mustCollect(t, h, 1000)
	if len(res.Collected) != 0 {
		t.Errorf("collected = %v, want empty (finalization queue is a root)", res.Collected)
	}
	fin, err = h.Finalize(2)
	if err != nil || !eqIDs(fin, []int{o8, o9}) {
		t.Errorf("Finalize(2) = %v, %v; want [%d %d]", fin, err, o8, o9)
	}
	res = mustCollect(t, h, 1000)
	if !eqIDs(res.Collected, []int{o8, o9}) {
		t.Errorf("collected = %v, want [%d %d]", res.Collected, o8, o9)
	}
	if got := h.Used(); got != 25 {
		t.Errorf("used = %d, want 25", got)
	}
	checkInvariants(t, h)
}

// Capacity: exactly full is allowed, one more byte is rejected.
func TestCapacityExactAndOverflow(t *testing.T) {
	h := newHeap(t, 10, 1, 0)
	mustAlloc(t, h, 10, 0, false)
	if got := h.Used(); got != 10 {
		t.Fatalf("used = %d, want 10", got)
	}
	if _, err := h.Alloc(1, 0, false); !errors.Is(err, ErrHeapFull) {
		t.Errorf("Alloc overflow: %v, want ErrHeapFull", err)
	}
	if _, err := h.NewRef(Weak, 0, 0, 1, 0); !errors.Is(err, ErrHeapFull) {
		t.Errorf("NewRef overflow: %v, want ErrHeapFull", err)
	}
	checkInvariants(t, h)
}

// Rejection reasons are checked in a fixed order and only the first
// one is reported.
func TestRejectOrder(t *testing.T) {
	h := newHeap(t, 100, 1, 0)
	o := mustAlloc(t, h, 10, 1, false)
	r := mustRef(t, h, Weak, 0, 0, 5, 0)

	// Invalid parameter beats heap-full.
	if _, err := h.Alloc(0, 0, false); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("Alloc(0): %v, want ErrInvalidParam", err)
	}
	if _, err := h.Alloc(1_000_001, 0, false); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("Alloc oversize: %v, want ErrInvalidParam", err)
	}
	if _, err := h.Alloc(1, 9, false); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("Alloc f=9: %v, want ErrInvalidParam", err)
	}
	// Invalid parameter beats not-found.
	if _, err := h.NewRef(Kind(9), 999, 0, 1, 0); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("NewRef bad kind + bad target: %v, want ErrInvalidParam", err)
	}
	if _, err := h.NewRef(Weak, 0, 0, 1, -1); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("NewRef now=-1: %v, want ErrInvalidParam", err)
	}
	if _, err := h.NewRef(Weak, 0, 0, 1, 1_000_000_000_000_001); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("NewRef now>1e15: %v, want ErrInvalidParam", err)
	}
	// Not-found beats clock regression and heap-full.
	if _, err := h.NewRef(Weak, 999, 0, 1, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("NewRef bad target: %v, want ErrNotFound", err)
	}
	if _, err := h.NewRef(Weak, 0, 999, 1, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("NewRef bad queue: %v, want ErrNotFound", err)
	}
	// Kind mismatch beats clock regression.
	if _, err := h.Get(o, 0); !errors.Is(err, ErrKindMismatch) {
		t.Errorf("Get on ordinary object: %v, want ErrKindMismatch", err)
	}
	if err := h.SetField(r, 0, 0); !errors.Is(err, ErrKindMismatch) {
		t.Errorf("SetField on reference: %v, want ErrKindMismatch", err)
	}
	// Field index out of range is an invalid parameter.
	if err := h.SetField(o, 8, 0); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("SetField i=8: %v, want ErrInvalidParam", err)
	}
	if err := h.SetField(o, 1, 0); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("SetField i>=f: %v, want ErrInvalidParam", err)
	}
	if err := h.SetField(o, 0, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetField bad target: %v, want ErrNotFound", err)
	}
	if err := h.SetField(999, 0, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetField bad object: %v, want ErrNotFound", err)
	}
	// Clock regression beats heap-full (capacity left is 85 < 100).
	if _, err := h.NewRef(Weak, 0, 0, 5, 5); err != nil {
		t.Fatalf("NewRef at now=5: %v", err)
	}
	if _, err := h.NewRef(Weak, 0, 0, 100, 4); !errors.Is(err, ErrClockRegression) {
		t.Errorf("NewRef regressed + oversized: %v, want ErrClockRegression", err)
	}
	if _, err := h.Collect(4); !errors.Is(err, ErrClockRegression) {
		t.Errorf("Collect regressed: %v, want ErrClockRegression", err)
	}
	if _, err := h.Get(r, 4); !errors.Is(err, ErrClockRegression) {
		t.Errorf("Get regressed: %v, want ErrClockRegression", err)
	}
	// Heap full is reported once the other checks pass.
	if _, err := h.NewRef(Weak, 0, 0, 100, 5); !errors.Is(err, ErrHeapFull) {
		t.Errorf("NewRef oversized: %v, want ErrHeapFull", err)
	}
	// Finalize parameter validation.
	if _, err := h.Finalize(-1); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("Finalize(-1): %v, want ErrInvalidParam", err)
	}
	if _, err := h.Finalize(1_000_001); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("Finalize(1e6+1): %v, want ErrInvalidParam", err)
	}
	if fin, err := h.Finalize(0); err != nil || len(fin) != 0 {
		t.Errorf("Finalize(0) = %v, %v; want empty", fin, err)
	}
	// Existence checks for roots, polls and getters.
	if err := h.SetRoot(999); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetRoot: %v, want ErrNotFound", err)
	}
	if err := h.ClearRoot(999); !errors.Is(err, ErrNotFound) {
		t.Errorf("ClearRoot: %v, want ErrNotFound", err)
	}
	if _, err := h.Poll(999); !errors.Is(err, ErrNotFound) {
		t.Errorf("Poll: %v, want ErrNotFound", err)
	}
	if _, err := h.Get(999, 5); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get missing: %v, want ErrNotFound", err)
	}
	// Constructor validation.
	for _, args := range [][3]int64{{0, 1, 0}, {1_000_000_000_001, 1, 0}, {1, 0, 0}, {1, 1_000_000_001, 0}, {1, 1, -1}, {1, 1, 1_000_000_001}} {
		if _, err := New(args[0], args[1], args[2]); !errors.Is(err, ErrInvalidParam) {
			t.Errorf("New%v: %v, want ErrInvalidParam", args, err)
		}
	}
	checkInvariants(t, h)
}

// Rejected operations must not change any state: no ids, queue
// numbers, clock or usage may advance.
func TestRejectedOpsDoNotChangeState(t *testing.T) {
	h := newHeap(t, 20, 1, 0)
	o := mustAlloc(t, h, 10, 0, false)
	if _, err := h.NewRef(Weak, 0, 0, 5, 7); err != nil {
		t.Fatal(err)
	}
	usedBefore := h.Used()

	// A batch of rejected operations.
	_, _ = h.Alloc(0, 0, false)          // invalid param
	_, _ = h.Alloc(100, 0, false)        // heap full
	_, _ = h.NewRef(Kind(3), 0, 0, 1, 8) // invalid kind
	_, _ = h.NewRef(Weak, 999, 0, 1, 8)  // missing target
	_, _ = h.NewRef(Weak, 0, 0, 1, 6)    // clock regression
	_, _ = h.NewRef(Weak, 0, 0, 100, 8)  // heap full
	_ = h.SetField(o, 3, 0)              // invalid index
	_ = h.SetField(999, 0, 0)            // missing object
	_, _ = h.Get(999, 8)                 // missing object
	_, _ = h.Collect(6)                  // clock regression
	_, _ = h.Finalize(-1)                // invalid n

	if got := h.Used(); got != usedBefore {
		t.Errorf("used = %d, want %d (unchanged)", got, usedBefore)
	}
	// Object ids continue where the last accepted operation stopped.
	next := mustAlloc(t, h, 1, 0, false)
	if next != 3 {
		t.Errorf("next id = %d, want 3 (rejected allocs consumed no ids)", next)
	}
	// Queue numbers were not consumed.
	if q := h.CreateQueue(); q != 1 {
		t.Errorf("next queue = %d, want 1", q)
	}
	// The clock still sits at 7: now == 7 is accepted.
	if _, err := h.Collect(7); err != nil {
		t.Errorf("Collect(7): %v (clock must not have advanced)", err)
	}
	checkInvariants(t, h)
}

// Weak references are cleared before the finalization phase: even
// though the target is resurrected afterwards, the weak reference
// stays cleared.
func TestWeakClearedBeforeFinalization(t *testing.T) {
	h := newHeap(t, 100, 10, 0)
	root := mustAlloc(t, h, 10, 0, false)
	if err := h.SetRoot(root); err != nil {
		t.Fatal(err)
	}
	f := mustAlloc(t, h, 10, 0, true) // unreachable, finalizable
	w := mustRef(t, h, Weak, f, 0, 5, 0)
	if err := h.SetRoot(w); err != nil {
		t.Fatal(err)
	}
	res := mustCollect(t, h, 0)
	if !eqIDs(res.WeakCleared, []int{w}) {
		t.Errorf("weak cleared = %v, want [%d]", res.WeakCleared, w)
	}
	if !eqIDs(res.FinalizeSelected, []int{f}) {
		t.Errorf("finalize selected = %v, want [%d]", res.FinalizeSelected, f)
	}
	if len(res.Collected) != 0 {
		t.Errorf("collected = %v, want empty (f resurrected)", res.Collected)
	}
	if got := mustGet(t, h, w, 0); got != 0 {
		t.Errorf("Get(weak) = %d, want 0 (stays cleared after resurrection)", got)
	}
	checkInvariants(t, h)
}

// A phantom reference to a resurrected object is not enqueued in the
// same round; after finalization and reclamation it is enqueued in
// the next round.
func TestPhantomEnqueuedAfterFinalize(t *testing.T) {
	h := newHeap(t, 100, 10, 0)
	root := mustAlloc(t, h, 10, 0, false)
	if err := h.SetRoot(root); err != nil {
		t.Fatal(err)
	}
	q := h.CreateQueue()
	f := mustAlloc(t, h, 10, 0, true)
	p := mustRef(t, h, Phantom, f, q, 5, 0)
	if err := h.SetRoot(p); err != nil {
		t.Fatal(err)
	}
	res := mustCollect(t, h, 0)
	if !eqIDs(res.FinalizeSelected, []int{f}) {
		t.Fatalf("finalize selected = %v, want [%d]", res.FinalizeSelected, f)
	}
	if len(res.FallbackCleared) != 0 {
		t.Errorf("fallback cleared = %v, want empty (target resurrected)", res.FallbackCleared)
	}
	if got := mustPoll(t, h, q); got != 0 {
		t.Errorf("poll = %d, want 0 (not enqueued this round)", got)
	}
	fin, err := h.Finalize(1)
	if err != nil || !eqIDs(fin, []int{f}) {
		t.Fatalf("Finalize(1) = %v, %v", fin, err)
	}
	res = mustCollect(t, h, 0)
	if !eqIDs(res.FallbackCleared, []int{p}) {
		t.Errorf("fallback cleared = %v, want [%d]", res.FallbackCleared, p)
	}
	if !eqIDs(res.Collected, []int{f}) {
		t.Errorf("collected = %v, want [%d]", res.Collected, f)
	}
	if got := mustPoll(t, h, q); got != p {
		t.Errorf("poll = %d, want %d", got, p)
	}
	checkInvariants(t, h)
}

// A reference object brought back by finalization resurrection is
// handled by the fallback phase in the same round.
func TestFallbackHandlesResurrectedRef(t *testing.T) {
	h := newHeap(t, 100, 10, 0)
	root := mustAlloc(t, h, 10, 0, false)
	if err := h.SetRoot(root); err != nil {
		t.Fatal(err)
	}
	q := h.CreateQueue()
	target := mustAlloc(t, h, 10, 0, false) // plain, unreachable
	f := mustAlloc(t, h, 10, 1, true)       // finalizable, unreachable
	w := mustRef(t, h, Weak, target, q, 5, 0)
	if err := h.SetField(f, 0, w); err != nil {
		t.Fatal(err)
	}
	res := mustCollect(t, h, 0)
	if !eqIDs(res.FinalizeSelected, []int{f}) {
		t.Errorf("finalize selected = %v, want [%d]", res.FinalizeSelected, f)
	}
	if !eqIDs(res.FallbackCleared, []int{w}) {
		t.Errorf("fallback cleared = %v, want [%d]", res.FallbackCleared, w)
	}
	if !eqIDs(res.Collected, []int{target}) {
		t.Errorf("collected = %v, want [%d]", res.Collected, target)
	}
	if got := mustPoll(t, h, q); got != w {
		t.Errorf("poll = %d, want %d", got, w)
	}
	checkInvariants(t, h)
}

// Within one phase references are processed by ascending id and
// enqueued in processing order.
func TestEnqueueOrderAscending(t *testing.T) {
	h := newHeap(t, 1000, 10, 0)
	root := mustAlloc(t, h, 10, 0, false)
	if err := h.SetRoot(root); err != nil {
		t.Fatal(err)
	}
	q := h.CreateQueue()
	var refs []int
	for i := 0; i < 5; i++ {
		target := mustAlloc(t, h, 10, 0, false)
		r := mustRef(t, h, Weak, target, q, 5, 0)
		if err := h.SetRoot(r); err != nil {
			t.Fatal(err)
		}
		refs = append(refs, r)
	}
	res := mustCollect(t, h, 0)
	if !eqIDs(res.WeakCleared, refs) {
		t.Errorf("weak cleared = %v, want ascending %v", res.WeakCleared, refs)
	}
	for _, want := range refs {
		if got := mustPoll(t, h, q); got != want {
			t.Errorf("poll = %d, want %d (FIFO in processing order)", got, want)
		}
	}
	checkInvariants(t, h)
}

// A reference without a queue is only cleared, never enqueued.
func TestClearedRefWithoutQueue(t *testing.T) {
	h := newHeap(t, 100, 10, 0)
	root := mustAlloc(t, h, 10, 0, false)
	if err := h.SetRoot(root); err != nil {
		t.Fatal(err)
	}
	target := mustAlloc(t, h, 10, 0, false)
	w := mustRef(t, h, Weak, target, 0, 5, 0)
	if err := h.SetRoot(w); err != nil {
		t.Fatal(err)
	}
	res := mustCollect(t, h, 0)
	if !eqIDs(res.WeakCleared, []int{w}) {
		t.Errorf("weak cleared = %v, want [%d]", res.WeakCleared, w)
	}
	if got := mustGet(t, h, w, 0); got != 0 {
		t.Errorf("Get = %d, want 0", got)
	}
	checkInvariants(t, h)
}

// Get refreshes the timestamp of soft references only.
func TestGetRefreshesSoftTimestampOnly(t *testing.T) {
	h := newHeap(t, 100, 10, 5)
	root := mustAlloc(t, h, 10, 0, false)
	if err := h.SetRoot(root); err != nil {
		t.Fatal(err)
	}
	ts := mustAlloc(t, h, 10, 0, false)
	tw := mustAlloc(t, h, 10, 0, false)
	s := mustRef(t, h, Soft, ts, 0, 5, 0)
	w := mustRef(t, h, Weak, tw, 0, 5, 0)
	for _, r := range []int{s, w} {
		if err := h.SetRoot(r); err != nil {
			t.Fatal(err)
		}
	}
	// used0 = 10+5+5 = 20, free = 80, threshold = floor(80/10)*5 = 40.
	if got := mustGet(t, h, s, 50); got != ts {
		t.Fatalf("Get(soft) = %d, want %d", got, ts)
	}
	if got := mustGet(t, h, w, 50); got != tw {
		t.Fatalf("Get(weak) = %d, want %d", got, tw)
	}
	// Soft timestamp is now 50: elapsed 60-50 = 10 <= 40 retains.
	// Without the refresh, elapsed 60 > 40 would clear.
	res := mustCollect(t, h, 60)
	if len(res.SoftCleared) != 0 {
		t.Errorf("soft cleared = %v, want empty (timestamp refreshed by Get)", res.SoftCleared)
	}
	// Weak references have no timestamp: Get does not protect them.
	if !eqIDs(res.WeakCleared, []int{w}) {
		t.Errorf("weak cleared = %v, want [%d]", res.WeakCleared, w)
	}
	checkInvariants(t, h)
}

func newHeap(t *testing.T, C, U, M int64) *Heap {
	t.Helper()
	h, err := New(C, U, M)
	if err != nil {
		t.Fatalf("New(%d,%d,%d): %v", C, U, M, err)
	}
	return h
}

// Strong marking must not propagate through reference targets: an
// object referenced only by weak/phantom references is reclaimed.
func TestRefsDoNotKeepTargetAlive(t *testing.T) {
	h := newHeap(t, 100, 10, 0)
	root := mustAlloc(t, h, 10, 0, false)
	if err := h.SetRoot(root); err != nil {
		t.Fatal(err)
	}
	tw := mustAlloc(t, h, 10, 0, false)
	tp := mustAlloc(t, h, 10, 0, false)
	w := mustRef(t, h, Weak, tw, 0, 5, 0)
	p := mustRef(t, h, Phantom, tp, 0, 5, 0)
	for _, r := range []int{w, p} {
		if err := h.SetRoot(r); err != nil {
			t.Fatal(err)
		}
	}
	res := mustCollect(t, h, 0)
	if !eqIDs(res.Collected, []int{tw, tp}) {
		t.Errorf("collected = %v, want [%d %d]", res.Collected, tw, tp)
	}
	if !eqIDs(res.WeakCleared, []int{w}) {
		t.Errorf("weak cleared = %v, want [%d]", res.WeakCleared, w)
	}
	if !eqIDs(res.FallbackCleared, []int{p}) {
		t.Errorf("fallback cleared = %v, want [%d]", res.FallbackCleared, p)
	}
	if got := mustGet(t, h, w, 0); got != 0 {
		t.Errorf("Get(weak) = %d, want 0", got)
	}
	if got := mustGet(t, h, p, 0); got != 0 {
		t.Errorf("Get(phantom) = %d, want 0", got)
	}
	checkInvariants(t, h)
}

// An unreachable reference object is swept itself and never processed
// or enqueued.
func TestUnreachableRefNotProcessed(t *testing.T) {
	h := newHeap(t, 100, 10, 0)
	q := h.CreateQueue()
	target := mustAlloc(t, h, 10, 0, false)
	w := mustRef(t, h, Weak, target, q, 5, 0) // not rooted
	res := mustCollect(t, h, 0)
	if !eqIDs(res.Collected, []int{target, w}) {
		t.Errorf("collected = %v, want [%d %d]", res.Collected, target, w)
	}
	if len(res.WeakCleared) != 0 {
		t.Errorf("weak cleared = %v, want empty", res.WeakCleared)
	}
	if got := mustPoll(t, h, q); got != 0 {
		t.Errorf("poll = %d, want 0 (nothing enqueued)", got)
	}
	checkInvariants(t, h)
}

// The soft-reference threshold: elapsed == floor(free/U)*M retains,
// elapsed one larger clears.
func TestSoftThresholdExact(t *testing.T) {
	h := newHeap(t, 100, 10, 5)
	root := mustAlloc(t, h, 10, 0, false)
	if err := h.SetRoot(root); err != nil {
		t.Fatal(err)
	}
	target := mustAlloc(t, h, 10, 0, false)
	s := mustRef(t, h, Soft, target, 0, 5, 0)
	if err := h.SetRoot(s); err != nil {
		t.Fatal(err)
	}
	// used0 = 10+5 = 15, free = 85, threshold = floor(85/10)*5 = 40.
	res := mustCollect(t, h, 40)
	if len(res.Collected) != 0 || len(res.SoftCleared) != 0 {
		t.Fatalf("elapsed == threshold must retain: %+v", res)
	}
	res = mustCollect(t, h, 41)
	if !eqIDs(res.SoftCleared, []int{s}) || !eqIDs(res.Collected, []int{target}) {
		t.Fatalf("elapsed == threshold+1 must clear: %+v", res)
	}
	checkInvariants(t, h)
}

// With M == 0 a soft reference is retained only when now equals its
// timestamp.
func TestSoftRetainZeroM(t *testing.T) {
	h := newHeap(t, 100, 10, 0)
	root := mustAlloc(t, h, 10, 0, false)
	if err := h.SetRoot(root); err != nil {
		t.Fatal(err)
	}
	target := mustAlloc(t, h, 10, 0, false)
	s := mustRef(t, h, Soft, target, 0, 5, 5)
	if err := h.SetRoot(s); err != nil {
		t.Fatal(err)
	}
	res := mustCollect(t, h, 5) // now == timestamp
	if len(res.SoftCleared) != 0 || len(res.Collected) != 0 {
		t.Fatalf("now == timestamp must retain: %+v", res)
	}
	res = mustCollect(t, h, 6) // now > timestamp
	if !eqIDs(res.SoftCleared, []int{s}) || !eqIDs(res.Collected, []int{target}) {
		t.Fatalf("now > timestamp must clear: %+v", res)
	}
	checkInvariants(t, h)
}

// free is computed once at the start of the soft phase: retaining one
// soft target must not shrink the budget for the next soft reference.
func TestSoftFreeComputedOnce(t *testing.T) {
	h := newHeap(t, 100, 1, 1)
	root := mustAlloc(t, h, 8, 0, false)
	if err := h.SetRoot(root); err != nil {
		t.Fatal(err)
	}
	a := mustAlloc(t, h, 41, 0, false)
	b := mustAlloc(t, h, 40, 0, false)
	s1 := mustRef(t, h, Soft, a, 0, 1, 0)
	s2 := mustRef(t, h, Soft, b, 0, 1, 0)
	for _, r := range []int{s1, s2} {
		if err := h.SetRoot(r); err != nil {
			t.Fatal(err)
		}
	}
	// used0 = 8+1+1 = 10, free = 90, threshold = floor(90/1)*1 = 90,
	// so elapsed 50 retains both. If free were recomputed after
	// retaining a (size 41), the threshold for s2 would drop to 49
	// and s2 would be cleared instead.
	res := mustCollect(t, h, 50)
	if len(res.SoftCleared) != 0 || len(res.Collected) != 0 {
		t.Fatalf("both soft targets must be retained: %+v", res)
	}
	checkInvariants(t, h)
}

// A retained soft target marks through its fields, which in turn
// protects the target of a downstream weak reference.
func TestSoftRetainProtectsDownstreamWeak(t *testing.T) {
	h := newHeap(t, 200, 10, 100)
	root := mustAlloc(t, h, 10, 0, false)
	if err := h.SetRoot(root); err != nil {
		t.Fatal(err)
	}
	a := mustAlloc(t, h, 10, 1, false)
	b := mustAlloc(t, h, 10, 0, false)
	if err := h.SetField(a, 0, b); err != nil {
		t.Fatal(err)
	}
	s := mustRef(t, h, Soft, a, 0, 5, 0)
	w := mustRef(t, h, Weak, b, 0, 5, 0)
	for _, r := range []int{s, w} {
		if err := h.SetRoot(r); err != nil {
			t.Fatal(err)
		}
	}
	res := mustCollect(t, h, 10)
	if len(res.Collected) != 0 {
		t.Errorf("collected = %v, want empty", res.Collected)
	}
	if len(res.WeakCleared) != 0 {
		t.Errorf("weak cleared = %v, want empty (b marked via soft target)", res.WeakCleared)
	}
	if got := mustGet(t, h, w, 10); got != b {
		t.Errorf("Get(weak) = %d, want %d", got, b)
	}
	checkInvariants(t, h)
}
