package swap

import (
	"errors"
	"reflect"
	"testing"
)

func mustNew(t *testing.T, n, k, maxCount int) *Ledger {
	t.Helper()
	l, err := NewLedger(n, k, maxCount)
	if err != nil {
		t.Fatalf("NewLedger(%d, %d, %d): %v", n, k, maxCount, err)
	}
	return l
}

func mustAlloc(t *testing.T, l *Ledger) int {
	t.Helper()
	s, err := l.Alloc()
	if err != nil {
		t.Fatalf("Alloc: %v", err)
	}
	return s
}

func mustAllocSeq(t *testing.T, l *Ledger, want []int) {
	t.Helper()
	for _, w := range want {
		if got := mustAlloc(t, l); got != w {
			t.Fatalf("Alloc = %d, want %d", got, w)
		}
	}
}

func wantErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("err = %v, want %v", got, want)
	}
}

func wantQueue(t *testing.T, l *Ledger, want []int) {
	t.Helper()
	if got := l.FreeClusters(); !reflect.DeepEqual(got, want) {
		t.Fatalf("FreeClusters = %v, want %v", got, want)
	}
}

func wantCurrent(t *testing.T, l *Ledger, cur, cursor int) {
	t.Helper()
	c, cu := l.Current()
	if c != cur || cu != cursor {
		t.Fatalf("Current = (%d, %d), want (%d, %d)", c, cu, cur, cursor)
	}
}

func TestConfigValidation(t *testing.T) {
	bad := [][3]int{
		{1, 1, 1}, {0, 1, 1}, {-5, 1, 1}, {1_000_001, 1, 1},
		{2, 0, 1}, {2, -1, 1}, {2, 1025, 1},
		{2, 1, 0}, {2, 1, -1}, {2, 1, 256},
	}
	for _, p := range bad {
		if l, err := NewLedger(p[0], p[1], p[2]); !errors.Is(err, ErrConfig) || l != nil {
			t.Errorf("NewLedger%v = (%v, %v), want (nil, ErrConfig)", p, l, err)
		}
	}
	good := [][3]int{
		{2, 1, 1}, {1_000_000, 1024, 255}, {2, 1024, 255}, {1_000_000, 1, 1},
	}
	for _, p := range good {
		if _, err := NewLedger(p[0], p[1], p[2]); err != nil {
			t.Errorf("NewLedger%v = %v, want nil", p, err)
		}
	}
}

// TestSpecExampleWalkthrough replays the worked example from the spec:
// N=12, K=4, Max=2 with clusters {1,2,3}, {4..7}, {8..11}.
func TestSpecExampleWalkthrough(t *testing.T) {
	l := mustNew(t, 12, 4, 2)
	wantQueue(t, l, []int{0, 1, 2})

	mustAllocSeq(t, l, []int{1, 2, 3, 4})
	wantCurrent(t, l, 1, 5)
	wantQueue(t, l, []int{2})

	if err := l.CacheDrop(2); err != nil {
		t.Fatal(err)
	}
	// Slot 2 is free again but lies before the cursor inside cur: not reused.
	if got := mustAlloc(t, l); got != 5 {
		t.Fatalf("Alloc = %d, want 5 (slot 2 must not be reused behind cursor)", got)
	}

	if err := l.CacheDrop(1); err != nil {
		t.Fatal(err)
	}
	if err := l.CacheDrop(3); err != nil {
		t.Fatal(err)
	}
	// Cluster 0 became completely free and is not cur: appended to tail.
	wantQueue(t, l, []int{2, 0})

	mustAllocSeq(t, l, []int{6, 7, 8})
	wantCurrent(t, l, 2, 9)
	mustAllocSeq(t, l, []int{9, 10, 11})

	// Cluster 2 exhausted; queue head is cluster 0, restart at its first slot.
	if got := mustAlloc(t, l); got != 1 {
		t.Fatalf("Alloc = %d, want 1", got)
	}
	wantCurrent(t, l, 0, 2)
	mustAllocSeq(t, l, []int{2, 3})

	if _, err := l.Alloc(); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("Alloc err = %v, want ErrNoSpace", err)
	}
}

// TestSpecExampleFallback replays the second spec example: with the queue
// empty, Alloc falls back to the globally lowest free slot and rewrites
// cur and cursor.
func TestSpecExampleFallback(t *testing.T) {
	l := mustNew(t, 12, 4, 2)
	mustAllocSeq(t, l, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11})
	wantCurrent(t, l, 2, 12)
	wantQueue(t, l, []int{})

	if err := l.CacheDrop(2); err != nil {
		t.Fatal(err)
	}
	if err := l.CacheDrop(6); err != nil {
		t.Fatal(err)
	}
	wantQueue(t, l, []int{})

	if got := mustAlloc(t, l); got != 2 {
		t.Fatalf("Alloc = %d, want 2 (global lowest free slot)", got)
	}
	wantCurrent(t, l, 0, 3)

	// Slot 3 is in use, so cur cannot serve; fallback again to slot 6.
	if got := mustAlloc(t, l); got != 6 {
		t.Fatalf("Alloc = %d, want 6", got)
	}
	wantCurrent(t, l, 1, 7)
}

func TestCountLimits(t *testing.T) {
	l := mustNew(t, 12, 4, 2)
	s := mustAlloc(t, l)
	if c, cache := l.Info(s); c != 0 || !cache {
		t.Fatalf("Info(%d) = (%d, %v), want (0, true)", s, c, cache)
	}
	// Max-1 then exactly Max.
	if err := l.Dup(s); err != nil {
		t.Fatal(err)
	}
	if err := l.Dup(s); err != nil {
		t.Fatal(err)
	}
	if c, _ := l.Info(s); c != 2 {
		t.Fatalf("count = %d, want 2 (== Max)", c)
	}
	wantErr(t, l.Dup(s), ErrOverflow)
	if c, _ := l.Info(s); c != 2 {
		t.Fatalf("count = %d after rejected Dup, want 2", c)
	}

	// Fork with a duplicate slot: cumulative simulation, whole batch rolls back.
	s2 := mustAlloc(t, l)
	if err := l.Dup(s2); err != nil {
		t.Fatal(err)
	}
	idx, err := l.Fork([]int{s2, s2})
	if idx != 1 || !errors.Is(err, ErrOverflow) {
		t.Fatalf("Fork = (%d, %v), want (1, ErrOverflow)", idx, err)
	}
	if c, _ := l.Info(s2); c != 1 {
		t.Fatalf("count = %d after rolled-back Fork, want 1", c)
	}

	// Release duplicates: first simulated Free frees the slot.
	if err := l.CacheDrop(s2); err != nil {
		t.Fatal(err)
	}
	idx, err = l.Release([]int{s2, s2})
	if idx != 1 || !errors.Is(err, ErrNotInUse) {
		t.Fatalf("Release = (%d, %v), want (1, ErrNotInUse)", idx, err)
	}
	if c, cache := l.Info(s2); c != 1 || cache {
		t.Fatalf("Info = (%d, %v) after rolled-back Release, want (1, false)", c, cache)
	}

	// Same with cache held: second Free underflows instead.
	if err := l.CacheAdd(s2); err != nil {
		t.Fatal(err)
	}
	idx, err = l.Release([]int{s2, s2})
	if idx != 1 || !errors.Is(err, ErrUnderflow) {
		t.Fatalf("Release = (%d, %v), want (1, ErrUnderflow)", idx, err)
	}
	if c, cache := l.Info(s2); c != 1 || !cache {
		t.Fatalf("Info = (%d, %v) after rolled-back Release, want (1, true)", c, cache)
	}
}

func TestCacheOnlySlotFreeUnderflows(t *testing.T) {
	l := mustNew(t, 8, 2, 2)
	s := mustAlloc(t, l) // count 0, held only by cache
	wantErr(t, l.Free(s), ErrUnderflow)
	if c, cache := l.Info(s); c != 0 || !cache {
		t.Fatalf("Info = (%d, %v), want (0, true) after rejected Free", c, cache)
	}
}

// TestRecycleRequiresBothZero: a slot is recycled only when count==0 AND
// cache==false, in either order.
func TestRecycleRequiresBothZero(t *testing.T) {
	l := mustNew(t, 8, 2, 2)

	// CacheDrop first, then Free.
	s1 := mustAlloc(t, l)
	if err := l.Dup(s1); err != nil {
		t.Fatal(err)
	}
	if err := l.CacheDrop(s1); err != nil {
		t.Fatal(err)
	}
	if got := l.Used(); got != 1 {
		t.Fatalf("Used = %d, want 1: count>0 keeps slot in use", got)
	}
	if err := l.Free(s1); err != nil {
		t.Fatal(err)
	}
	if got := l.Used(); got != 0 {
		t.Fatalf("Used = %d, want 0 after both reached zero", got)
	}

	// Free first, then CacheDrop.
	s2 := mustAlloc(t, l)
	if err := l.Dup(s2); err != nil {
		t.Fatal(err)
	}
	if err := l.Free(s2); err != nil {
		t.Fatal(err)
	}
	if got := l.Used(); got != 1 {
		t.Fatalf("Used = %d, want 1: cache keeps slot in use", got)
	}
	if err := l.CacheDrop(s2); err != nil {
		t.Fatal(err)
	}
	if got := l.Used(); got != 0 {
		t.Fatalf("Used = %d, want 0 after both reached zero", got)
	}
	if l.Used()+l.FreeSlots() != 7 {
		t.Fatalf("Used+FreeSlots = %d, want N-1 = 7", l.Used()+l.FreeSlots())
	}
}

func TestSlotZeroNeverAllocated(t *testing.T) {
	for _, cfg := range [][3]int{{2, 1, 1}, {17, 3, 2}, {64, 1024, 1}} {
		l := mustNew(t, cfg[0], cfg[1], cfg[2])
		seen := make(map[int]bool)
		for {
			s, err := l.Alloc()
			if errors.Is(err, ErrNoSpace) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if s == 0 {
				t.Fatalf("N=%d: slot 0 was allocated", cfg[0])
			}
			if seen[s] {
				t.Fatalf("N=%d: slot %d allocated twice", cfg[0], s)
			}
			seen[s] = true
		}
		if len(seen) != cfg[0]-1 {
			t.Fatalf("N=%d: allocated %d slots, want %d", cfg[0], len(seen), cfg[0]-1)
		}
	}
}

// TestKEqualsOne: with K=1 cluster 0 would only hold slot 0, so it does not
// exist; clusters are 1..N-1 with one slot each.
func TestKEqualsOne(t *testing.T) {
	l := mustNew(t, 5, 1, 1)
	wantQueue(t, l, []int{1, 2, 3, 4})
	mustAllocSeq(t, l, []int{1, 2, 3, 4})
	if _, err := l.Alloc(); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("Alloc err = %v, want ErrNoSpace", err)
	}
}

// TestQueueHeadBeatsLowestCluster: when cur is exhausted the queue head is
// adopted, not the lowest-numbered free cluster.
func TestQueueHeadBeatsLowestCluster(t *testing.T) {
	l := mustNew(t, 12, 4, 2)
	mustAllocSeq(t, l, []int{1, 2, 3, 4})
	for _, s := range []int{1, 2, 3} {
		if err := l.CacheDrop(s); err != nil {
			t.Fatal(err)
		}
	}
	// Cluster 0 is completely free and queued behind cluster 2.
	wantQueue(t, l, []int{2, 0})
	mustAllocSeq(t, l, []int{5, 6, 7})
	// cur (cluster 1) exhausted: adopt queue head cluster 2, not cluster 0.
	if got := mustAlloc(t, l); got != 8 {
		t.Fatalf("Alloc = %d, want 8 (queue head cluster 2)", got)
	}
	wantCurrent(t, l, 2, 9)
}

// TestCurFullyFreeLifecycle: cur does not enter the queue when it becomes
// completely free; it is appended only when replaced, and may be re-adopted
// later starting from its first slot.
func TestCurFullyFreeLifecycle(t *testing.T) {
	l := mustNew(t, 5, 2, 2)
	// Clusters: {1}, {2,3}, {4}; queue [0,1,2].
	mustAllocSeq(t, l, []int{1, 2})
	wantCurrent(t, l, 1, 3)
	if err := l.CacheDrop(2); err != nil {
		t.Fatal(err)
	}
	// Cluster 1 (cur) is completely free but must not be queued.
	wantQueue(t, l, []int{2})

	if got := mustAlloc(t, l); got != 3 {
		t.Fatalf("Alloc = %d, want 3", got)
	}
	// Cursor reached the end of cur; cur is queued on replacement...
	if got := mustAlloc(t, l); got != 4 {
		t.Fatalf("Alloc = %d, want 4", got)
	}
	wantCurrent(t, l, 2, 5)
	// ...and re-adopted from the queue, restarting at its first slot.
	if got := mustAlloc(t, l); got != 2 {
		t.Fatalf("Alloc = %d, want 2 (cur re-adopted, first slot)", got)
	}
	wantCurrent(t, l, 1, 3)
}

// TestCurEnqueuedOnReplace checks the enqueue order when cur becomes
// completely free and is later replaced: it goes to the tail at that moment.
func TestCurEnqueuedOnReplace(t *testing.T) {
	l := mustNew(t, 9, 4, 2)
	// Clusters: {1,2,3}, {4,5,6,7}, {8}; queue [0,1,2].
	mustAllocSeq(t, l, []int{1, 2, 3, 4})
	wantQueue(t, l, []int{2})
	for _, s := range []int{1, 2, 3} {
		if err := l.CacheDrop(s); err != nil {
			t.Fatal(err)
		}
	}
	wantQueue(t, l, []int{2, 0})
	if err := l.CacheDrop(4); err != nil {
		t.Fatal(err)
	}
	// cur (cluster 1) is completely free but not queued while current.
	wantQueue(t, l, []int{2, 0})
	mustAllocSeq(t, l, []int{5, 6, 7})
	for _, s := range []int{5, 6, 7} {
		if err := l.CacheDrop(s); err != nil {
			t.Fatal(err)
		}
	}
	// cur is completely free again with the cursor at its end, still not queued.
	wantQueue(t, l, []int{2, 0})
	// Replacement appends cur (cluster 1) to the tail, then pops cluster 2.
	if got := mustAlloc(t, l); got != 8 {
		t.Fatalf("Alloc = %d, want 8", got)
	}
	wantQueue(t, l, []int{0, 1})
	wantCurrent(t, l, 2, 9)
}

// stateSnapshot captures the full observable state for no-change assertions.
type stateSnapshot struct {
	count  []uint8
	cache  []bool
	used   int
	cur    int
	cursor int
	queue  []int
}

func grab(l *Ledger) stateSnapshot {
	return stateSnapshot{
		count:  append([]uint8(nil), l.count...),
		cache:  append([]bool(nil), l.cache...),
		used:   l.used,
		cur:    l.cur,
		cursor: l.cursor,
		queue:  l.queue.snapshot(),
	}
}

func wantStateUnchanged(t *testing.T, before, after stateSnapshot) {
	t.Helper()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("state changed:\nbefore %+v\nafter  %+v", before, after)
	}
}

func TestBatchAtomicityAndEnqueueOrder(t *testing.T) {
	l := mustNew(t, 12, 4, 3)
	mustAllocSeq(t, l, []int{1, 2, 3, 4, 5, 6, 7, 8})
	// Give slots 1..7 a count of 1 and drop their cache flags.
	if idx, err := l.Fork([]int{1, 2, 3, 4, 5, 6, 7}); idx != -1 || err != nil {
		t.Fatalf("Fork = (%d, %v)", idx, err)
	}
	for s := 1; s <= 7; s++ {
		if err := l.CacheDrop(s); err != nil {
			t.Fatal(err)
		}
	}
	wantQueue(t, l, []int{})

	// One atomic batch frees slots 1..7 in order; clusters 0 and 1 become
	// completely free in exactly that element order.
	if idx, err := l.Release([]int{1, 2, 3, 4, 5, 6, 7}); idx != -1 || err != nil {
		t.Fatalf("Release = (%d, %v)", idx, err)
	}
	wantQueue(t, l, []int{0, 1})
	if got := l.Used(); got != 1 {
		t.Fatalf("Used = %d, want 1", got)
	}

	// A failing batch changes nothing at all.
	before := grab(l)
	idx, err := l.Release([]int{1, 2, 3}) // slot 1 is free again -> fails at 0
	if idx != 0 || !errors.Is(err, ErrNotInUse) {
		t.Fatalf("Release = (%d, %v), want (0, ErrNotInUse)", idx, err)
	}
	wantStateUnchanged(t, before, grab(l))

	// Fork failing mid-batch (range error) also changes nothing.
	idx, err = l.Fork([]int{8, 12})
	if idx != 1 || !errors.Is(err, ErrRange) {
		t.Fatalf("Fork = (%d, %v), want (1, ErrRange)", idx, err)
	}
	wantStateUnchanged(t, before, grab(l))
}

func TestErrNoSpaceKeepsState(t *testing.T) {
	l := mustNew(t, 6, 2, 1)
	mustAllocSeq(t, l, []int{1, 2, 3, 4, 5})
	before := grab(l)
	if _, err := l.Alloc(); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("Alloc err = %v, want ErrNoSpace", err)
	}
	wantStateUnchanged(t, before, grab(l))
}

func TestErrorPrecedence(t *testing.T) {
	l := mustNew(t, 8, 2, 2)
	free := 5 // never allocated

	// ErrRange beats everything, even for slot 0 and beyond N-1.
	for _, s := range []int{-1, 0, 8, 100} {
		wantErr(t, l.Dup(s), ErrRange)
		wantErr(t, l.Free(s), ErrRange)
		wantErr(t, l.CacheAdd(s), ErrRange)
		wantErr(t, l.CacheDrop(s), ErrRange)
	}
	// ErrNotInUse beats the operation-specific errors on free slots.
	wantErr(t, l.Dup(free), ErrNotInUse)
	wantErr(t, l.Free(free), ErrNotInUse)
	wantErr(t, l.CacheAdd(free), ErrNotInUse)
	wantErr(t, l.CacheDrop(free), ErrNotInUse)

	s := mustAlloc(t, l) // count 0, cache true
	wantErr(t, l.CacheAdd(s), ErrExists)
	wantErr(t, l.Free(s), ErrUnderflow)
	if err := l.Dup(s); err != nil {
		t.Fatal(err)
	}
	if err := l.CacheDrop(s); err != nil {
		t.Fatal(err)
	}
	// In use via count only: cache flag already false.
	wantErr(t, l.CacheDrop(s), ErrNoCache)
	if err := l.Dup(s); err != nil {
		t.Fatal(err)
	}
	wantErr(t, l.Dup(s), ErrOverflow)
}

func TestRejectedOpsKeepState(t *testing.T) {
	l := mustNew(t, 8, 2, 2)
	s := mustAlloc(t, l)
	if err := l.Dup(s); err != nil {
		t.Fatal(err)
	}
	ops := []func() error{
		func() error { return l.Dup(0) },
		func() error { return l.Dup(3) },  // free slot
		func() error { return l.Free(9) }, // range
		func() error { return l.Free(3) }, // free slot
		func() error { return l.CacheAdd(s) },
		func() error { return l.CacheDrop(3) },
		func() error { return l.Free(s) }, // count 1 -> ok, not an error
	}
	for i, op := range ops[:6] {
		before := grab(l)
		if err := op(); err == nil {
			t.Fatalf("op %d unexpectedly succeeded", i)
		}
		wantStateUnchanged(t, before, grab(l))
	}
	_ = ops
}

// TestProbesBound verifies the complexity counters at N=10^6, K=8.
func TestProbesBound(t *testing.T) {
	const n, k = 1_000_000, 8
	l := mustNew(t, n, k, 1)
	numClusters := (n-1)/k + 1
	log2 := 0
	for (1 << log2) < numClusters {
		log2++
	}
	fallbackBound := int64(2*log2+2) + k

	// Fill the pool; every Alloc examines at most K slots on the
	// non-fallback paths, and the tree bound on the fallback path.
	for i := 0; i < n-1; i++ {
		before := l.probes
		if _, err := l.Alloc(); err != nil {
			t.Fatalf("Alloc %d: %v", i, err)
		}
		delta := l.probes - before
		if l.lastPath == 3 {
			if delta > fallbackBound {
				t.Fatalf("fallback Alloc probes = %d, want <= %d", delta, fallbackBound)
			}
		} else if delta > k {
			t.Fatalf("non-fallback Alloc probes = %d, want <= %d", delta, k)
		}
	}

	// Free and CacheDrop (incl. queue maintenance) touch no probes.
	before := l.probes
	if err := l.CacheDrop(2); err != nil {
		t.Fatal(err)
	}
	if err := l.CacheDrop(3); err != nil {
		t.Fatal(err)
	}
	if l.probes != before {
		t.Fatalf("CacheDrop changed probes by %d, want 0", l.probes-before)
	}

	// Fallback Alloc with an empty queue examines O(log clusters).
	before = l.probes
	s, err := l.Alloc()
	if err != nil {
		t.Fatal(err)
	}
	if s != 2 {
		t.Fatalf("Alloc = %d, want 2", s)
	}
	if l.lastPath != 3 {
		t.Fatalf("lastPath = %d, want 3 (fallback)", l.lastPath)
	}
	if delta := l.probes - before; delta > fallbackBound {
		t.Fatalf("fallback probes = %d, want <= %d", delta, fallbackBound)
	}
	t.Logf("N=%d K=%d clusters=%d fallback probes=%d bound=%d",
		n, k, numClusters, l.probes-before, fallbackBound)
}
