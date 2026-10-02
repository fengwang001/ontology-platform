package changebuffer

import (
	"errors"
	"testing"
)

type pageSnap struct {
	inPool   bool
	used     int64
	bufBytes int64
	queue    []QueuedOp
	entries  []Entry
}

func snapshot(cb *ChangeBuffer) map[int]pageSnap {
	out := make(map[int]pageSnap)
	cb.mu.Lock()
	defer cb.mu.Unlock()
	for page, p := range cb.pages {
		s := pageSnap{inPool: p.inPool, used: p.used, bufBytes: p.bufBytes}
		s.queue = append(s.queue, p.queue...)
		for k, en := range p.entries {
			s.entries = append(s.entries, Entry{Key: k, Size: en.size, Marked: en.marked})
		}
		out[page] = s
	}
	return out
}

func mustNew(t *testing.T, S, Kp, G int64) *ChangeBuffer {
	t.Helper()
	cb, err := New(S, Kp, G)
	if err != nil {
		t.Fatalf("New(%d,%d,%d): %v", S, Kp, G, err)
	}
	return cb
}

func entryMap(es []Entry) map[string]Entry {
	m := make(map[string]Entry, len(es))
	for _, e := range es {
		m[e.Key] = e
	}
	return m
}

// Spec walkthrough: S=1024, Kp=3, G=150, pages 7/8/9.
func TestSpecWalkthrough(t *testing.T) {
	cb := mustNew(t, 1024, 3, 150)

	d, err := cb.Op(7, Insert, "a", 100)
	if err != nil || d != DecisionBuffered {
		t.Fatalf("op1: d=%d err=%v", d, err)
	}
	d, err = cb.Op(7, Insert, "b", 40)
	if err != nil || d != DecisionForceMerged {
		t.Fatalf("op2: d=%d err=%v", d, err)
	}
	if in, _ := cb.InPool(7); !in {
		t.Fatalf("page 7 should be resident")
	}
	if u, _ := cb.Used(7); u != 140 {
		t.Fatalf("used=%d want 140", u)
	}

	if err := cb.Evict(7); err != nil {
		t.Fatalf("evict: %v", err)
	}
	if d, err = cb.Op(7, DeleteMark, "a", 0); err != nil || d != DecisionBuffered {
		t.Fatalf("delete mark: d=%d err=%v", d, err)
	}
	if d, err = cb.Op(7, Insert, "a", 90); err != nil || d != DecisionBuffered {
		t.Fatalf("reinsert a: d=%d err=%v", d, err)
	}

	view, err := cb.View(7)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	em := entryMap(view)
	if len(em) != 2 || em["a"] != (Entry{"a", 100, false}) || em["b"] != (Entry{"b", 40, false}) {
		t.Fatalf("view=%v", view)
	}
	if in, _ := cb.InPool(7); in {
		t.Fatalf("View must not change residency")
	}

	if err := cb.Load(7); err != nil {
		t.Fatalf("load: %v", err)
	}
	view2, _ := cb.View(7)
	if len(view2) != len(view) {
		t.Fatalf("post-load view differs")
	}
	for i := range view {
		if view[i] != view2[i] {
			t.Fatalf("post-load view[%d]=%v want %v", i, view2[i], view[i])
		}
	}
	if u, _ := cb.Used(7); u != 140 {
		t.Fatalf("used after load=%d want 140", u)
	}

	// Global budget: page 8 buffers 100; page 9's 60 exceeds G=150 and
	// is force-merged.
	if d, err = cb.Op(8, Insert, "x", 100); err != nil || d != DecisionBuffered {
		t.Fatalf("page8: d=%d err=%v", d, err)
	}
	if d, err = cb.Op(9, Insert, "y", 60); err != nil || d != DecisionForceMerged {
		t.Fatalf("page9: d=%d err=%v", d, err)
	}
	if in, _ := cb.InPool(9); !in {
		t.Fatalf("page 9 should be resident")
	}
	if g := cb.GlobalBufBytes(); g != 100 {
		t.Fatalf("globalBuf=%d want 100", g)
	}
	if err := cb.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

func TestLocalBufBoundaryEqualAndPlusOne(t *testing.T) {
	// S=1024 empty page => bucket 3 => lb = 128.
	cb := mustNew(t, 1024, 10, 1_000_000_000_000)
	if d, err := cb.Op(1, Insert, "k", 128); err != nil || d != DecisionBuffered {
		t.Fatalf("==lb: d=%d err=%v", d, err)
	}

	cb2 := mustNew(t, 1024, 10, 1_000_000_000_000)
	if d, err := cb2.Op(1, Insert, "k", 129); err != nil || d != DecisionForceMerged {
		t.Fatalf("lb+1: d=%d err=%v", d, err)
	}
}

func TestQuotaAccumulatesAcrossQueue(t *testing.T) {
	// lb=128: 70 buffers; the second 70 is judged on 70+70=140, not 70 alone.
	cb := mustNew(t, 1024, 10, 1_000_000_000_000)
	if d, _ := cb.Op(1, Insert, "a", 70); d != DecisionBuffered {
		t.Fatalf("first 70: %d", d)
	}
	if d, _ := cb.Op(1, Insert, "b", 70); d != DecisionForceMerged {
		t.Fatalf("second 70 must force-merge, got %d", d)
	}
}

func TestGlobalBoundaryEqualAndPlusOne(t *testing.T) {
	cb := mustNew(t, 1024, 10, 100)
	if d, _ := cb.Op(1, Insert, "a", 60); d != DecisionBuffered {
		t.Fatalf("a: %d", d)
	}
	if d, _ := cb.Op(2, Insert, "b", 40); d != DecisionBuffered {
		t.Fatalf("b at global==G: %d", d)
	}
	// Local quota allows 1 (lb=128) but global 101 > 100: force merge.
	if d, _ := cb.Op(3, Insert, "c", 1); d != DecisionForceMerged {
		t.Fatalf("c global+1: %d", d)
	}
	if g := cb.GlobalBufBytes(); g != 100 {
		t.Fatalf("global=%d", g)
	}

	// Load releases page 1's 60 bytes; page 4 can buffer 50 again.
	if err := cb.Load(1); err != nil {
		t.Fatal(err)
	}
	if g := cb.GlobalBufBytes(); g != 40 {
		t.Fatalf("global after load=%d", g)
	}
	if d, _ := cb.Op(4, Insert, "d", 50); d != DecisionBuffered {
		t.Fatalf("d after release: %d", d)
	}
	if g := cb.GlobalBufBytes(); g != 90 {
		t.Fatalf("global=%d want 90", g)
	}
}

func TestForceMergeReleasesGlobalQuota(t *testing.T) {
	cb := mustNew(t, 1024, 2, 130)
	cb.Op(1, Insert, "a", 100) // buffered, global=100
	// Second insert on page 1: 100+100 > lb 128 => force merge; the 100
	// bytes are released back to the global budget.
	if d, err := cb.Op(1, Insert, "b", 100); err != nil || d != DecisionForceMerged {
		t.Fatalf("force merge: d=%d err=%v", d, err)
	}
	if g := cb.GlobalBufBytes(); g != 0 {
		t.Fatalf("global after force merge=%d", g)
	}
	// Another page can now buffer.
	if d, _ := cb.Op(2, Insert, "c", 120); d != DecisionBuffered {
		t.Fatalf("page2 after release: %d", d)
	}
}

func TestMarkAndPurgeUnrestrictedByBudgetAndSpace(t *testing.T) {
	// Saturate the global budget with another page.
	cb := mustNew(t, 1024, 4, 100)
	cb.Op(1, Insert, "x", 100) // global=100 == G

	// Full non-resident page (materialized then evicted): F=0, bucket 0.
	cb.Op(5, Insert, "z", 1024) // force-merged resident
	cb.Evict(5)
	// DeleteMark and Purge buffer despite global saturation and F=0.
	if d, err := cb.Op(5, DeleteMark, "z", 0); err != nil || d != DecisionBuffered {
		t.Fatalf("delete mark: d=%d err=%v", d, err)
	}
	if d, err := cb.Op(5, Purge, "z", 0); err != nil || d != DecisionBuffered {
		t.Fatalf("purge: d=%d err=%v", d, err)
	}
	if b, _ := cb.BufBytes(5); b != 0 {
		t.Fatalf("marks must not add bufBytes, got %d", b)
	}

	// Kp still binds marks: fresh page with Kp=2 hits the cap.
	cb2 := mustNew(t, 1024, 2, 1_000_000_000_000)
	cb2.Op(7, DeleteMark, "a", 0)
	cb2.Op(7, Purge, "a", 0)
	if d, _ := cb2.Op(7, DeleteMark, "a", 0); d != DecisionForceMerged {
		t.Fatalf("queue==Kp must force merge, got %d", d)
	}
}

func TestKpExactCapForcesMerge(t *testing.T) {
	cb := mustNew(t, 1024, 3, 1_000_000_000_000)
	for i := 0; i < 3; i++ {
		if d, _ := cb.Op(1, DeleteMark, "k", 0); d != DecisionBuffered {
			t.Fatalf("op %d: %d", i, d)
		}
	}
	if d, _ := cb.Op(1, Purge, "k", 0); d != DecisionForceMerged {
		t.Fatalf("4th op must force merge, got %d", d)
	}
	if in, _ := cb.InPool(1); !in {
		t.Fatalf("page should be resident")
	}
}

func TestBucketBoundaries(t *testing.T) {
	// S=1024: F=32 -> 32F==S (not <) => bucket 1
	//         F=64 -> 16F==S => bucket 2
	//         F=128 -> 8F==S => bucket 3
	cb := mustNew(t, 1024, 10, 1_000_000_000_000)
	setup := func(page int, used int64) {
		cb.Op(page, Insert, "z", used) // force-merges for these used values
		cb.Evict(page)
	}
	setup(1, 1024-32)
	setup(2, 1024-64)
	setup(3, 1024-128)
	setup(4, 1024)
	if b, _ := cb.Bucket(1); b != 1 {
		t.Fatalf("F=32 bucket=%d want 1", b)
	}
	if b, _ := cb.Bucket(2); b != 2 {
		t.Fatalf("F=64 bucket=%d want 2", b)
	}
	if b, _ := cb.Bucket(3); b != 3 {
		t.Fatalf("F=128 bucket=%d want 3", b)
	}
	if b, _ := cb.Bucket(4); b != 0 {
		t.Fatalf("F=0 bucket=%d want 0", b)
	}
	lbs := []int64{0, 32, 64, 128}
	for i, want := range lbs {
		if got, _ := cb.LowerBound(i); got != want {
			t.Fatalf("lb(%d)=%d want %d", i, got, want)
		}
	}

	// lb flooring with S not divisible by 32/16/8: S=100 -> 3,6,12.
	cb2 := mustNew(t, 100, 10, 1_000_000_000_000)
	wants := []int64{0, 3, 6, 12}
	for i, w := range wants {
		if got, _ := cb2.LowerBound(i); got != w {
			t.Fatalf("S=100 lb(%d)=%d want %d", i, got, w)
		}
	}
}

func TestInsertExistingClearsMarkWithoutSpace(t *testing.T) {
	cb := mustNew(t, 1024, 10, 1_000_000_000_000)
	cb.Op(1, Insert, "a", 1024) // resident, page full
	cb.Op(1, DeleteMark, "a", 0)
	// Insert clears the mark without changing size, even on a full page.
	if _, err := cb.Op(1, Insert, "a", 1024); err != nil {
		t.Fatalf("reinsert existing: %v", err)
	}
	view, _ := cb.View(1)
	if len(view) != 1 || view[0] != (Entry{"a", 1024, false}) {
		t.Fatalf("view=%v", view)
	}

	// Same through a buffered merge, including marked-existing case.
	cb.Op(1, DeleteMark, "a", 0)
	cb.Evict(1)
	// The page is full (F=0, lb=0), so the reinsert force-merges; the
	// merge applies it to the existing marked key and clears the mark.
	if d, err := cb.Op(1, Insert, "a", 90); err != nil || d != DecisionForceMerged {
		t.Fatalf("force-merge reinsert: d=%d err=%v", d, err)
	}
	if err := cb.Load(1); err != nil {
		t.Fatal(err)
	}
	view, _ = cb.View(1)
	if view[0] != (Entry{"a", 1024, false}) {
		t.Fatalf("after merge: %v", view)
	}
}

func TestNoopMarkAndPurge(t *testing.T) {
	cb := mustNew(t, 1024, 10, 1_000_000_000_000)
	// DeleteMark on missing key: noop. Purge on unmarked key: noop.
	cb.Op(1, DeleteMark, "missing", 0)
	cb.Load(1)
	if v, _ := cb.View(1); len(v) != 0 {
		t.Fatalf("delete-mark missing created entry: %v", v)
	}
	cb.Op(1, Insert, "a", 10)
	cb.Op(1, Purge, "a", 0) // a exists but is not marked
	v, _ := cb.View(1)
	if len(v) != 1 || v[0] != (Entry{"a", 10, false}) {
		t.Fatalf("purge unmarked changed state: %v", v)
	}
	if u, _ := cb.Used(1); u != 10 {
		t.Fatalf("used=%d want 10", u)
	}
}

func TestMarkThenPurgeReleasesSpace(t *testing.T) {
	cb := mustNew(t, 100, 10, 1_000_000_000_000)
	// Resident path.
	cb.Op(1, Insert, "a", 60)
	cb.Op(1, Insert, "b", 40) // page full
	cb.Op(1, DeleteMark, "a", 0)
	if _, err := cb.Op(1, Insert, "c", 5); !errors.Is(err, ErrPageSpace) {
		t.Fatalf("expected space error, got %v", err)
	}
	cb.Op(1, Purge, "a", 0)
	if u, _ := cb.Used(1); u != 40 {
		t.Fatalf("used after purge=%d want 40", u)
	}
	if _, err := cb.Op(1, Insert, "c", 5); err != nil {
		t.Fatalf("insert after release: %v", err)
	}

	// Buffered merge path.
	cb2 := mustNew(t, 100, 10, 1_000_000_000_000)
	cb2.Op(2, Insert, "a", 60)
	cb2.Op(2, Insert, "b", 40)
	cb2.Evict(2)
	cb2.Op(2, DeleteMark, "a", 0)
	cb2.Op(2, Purge, "a", 0)
	cb2.Op(2, Insert, "c", 5) // buffered, admitted against F=0 bucket lb=0
	if err := cb2.Load(2); err != nil {
		t.Fatalf("load: %v", err)
	}
	v, _ := cb2.View(2)
	em := entryMap(v)
	if len(em) != 2 || em["b"] != (Entry{"b", 40, false}) || em["c"] != (Entry{"c", 5, false}) {
		t.Fatalf("after buffered purge+insert: %v", v)
	}
}

func TestSameKeyOrderDependence(t *testing.T) {
	// Order A: Insert a, DeleteMark a, Purge a  -> entry gone.
	cbA := mustNew(t, 100, 10, 1_000_000_000_000)
	cbA.Op(1, Insert, "a", 50)
	cbA.Evict(1)
	cbA.Op(1, DeleteMark, "a", 0)
	cbA.Op(1, Purge, "a", 0)
	cbA.Load(1)
	if v, _ := cbA.View(1); len(v) != 0 {
		t.Fatalf("order A: expected empty, got %v", v)
	}

	// Order B: Insert a, Purge a, DeleteMark a -> a remains unmarked.
	cbB := mustNew(t, 100, 10, 1_000_000_000_000)
	cbB.Op(1, Insert, "a", 50)
	cbB.Evict(1)
	cbB.Op(1, Purge, "a", 0)      // noop: not marked
	cbB.Op(1, DeleteMark, "a", 0) // now marked
	cbB.Load(1)
	v, _ := cbB.View(1)
	if len(v) != 1 || v[0] != (Entry{"a", 50, true}) {
		t.Fatalf("order B: %v", v)
	}

	// Order C: DeleteMark, Insert (clears mark), Purge -> survives.
	cbC := mustNew(t, 100, 10, 1_000_000_000_000)
	cbC.Op(1, Insert, "a", 50)
	cbC.Evict(1)
	cbC.Op(1, DeleteMark, "a", 0)
	cbC.Op(1, Insert, "a", 10)
	cbC.Op(1, Purge, "a", 0) // noop: mark was cleared, size stays 50
	cbC.Load(1)
	v, _ = cbC.View(1)
	if len(v) != 1 || v[0] != (Entry{"a", 50, false}) {
		t.Fatalf("order C: %v", v)
	}
}

func TestForceMergeRejectLeavesStateUntouched(t *testing.T) {
	// Full non-resident page with a queued mark (forces merge on next op);
	// inserting one byte on the full page must be rejected with no change.
	cb := mustNew(t, 100, 10, 1_000_000_000_000)
	cb.Op(1, Insert, "z", 100)
	cb.Evict(1)
	cb.Op(1, DeleteMark, "z", 0) // queued; any next op force-merges

	before := snapshot(cb)
	_, err := cb.Op(1, Insert, "new", 1)
	if !errors.Is(err, ErrPageSpace) {
		t.Fatalf("want ErrPageSpace, got %v", err)
	}
	after := snapshot(cb)
	p0, p1 := before[1], after[1]
	if p1.inPool || p1.used != p0.used || p1.bufBytes != p0.bufBytes ||
		len(p1.queue) != len(p0.queue) || len(p1.entries) != len(p0.entries) {
		t.Fatalf("state changed on reject:\nbefore=%+v\nafter =%+v", p0, p1)
	}
	if in, _ := cb.InPool(1); in {
		t.Fatalf("page must stay non-resident")
	}
	if q, _ := cb.Queue(1); len(q) != 1 || q[0].Kind != DeleteMark {
		t.Fatalf("queue must be preserved, got %v", q)
	}
	// Global counters untouched too.
	if g := cb.GlobalBufBytes(); g != 0 {
		t.Fatalf("global=%d", g)
	}
}

func TestDirectApplySpaceReject(t *testing.T) {
	cb := mustNew(t, 100, 10, 1_000_000_000_000)
	cb.Op(1, Insert, "a", 60) // resident
	before := snapshot(cb)
	if _, err := cb.Op(1, Insert, "b", 41); !errors.Is(err, ErrPageSpace) {
		t.Fatalf("want space error, got %v", err)
	}
	after := snapshot(cb)
	if after[1].used != before[1].used || len(after[1].entries) != len(before[1].entries) {
		t.Fatalf("state changed on direct reject")
	}
	// One byte less fits (F=40 >= 40).
	if _, err := cb.Op(1, Insert, "b", 40); err != nil {
		t.Fatalf("boundary insert: %v", err)
	}
}

func TestEvictThenBufferAgain(t *testing.T) {
	cb := mustNew(t, 1024, 5, 1_000_000_000_000)
	cb.Op(1, Insert, "a", 100) // force-merges, resident
	cb.Evict(1)
	if d, err := cb.Op(1, Insert, "b", 10); err != nil || d != DecisionBuffered {
		t.Fatalf("post-evict buffer: d=%d err=%v", d, err)
	}
	if err := cb.Evict(1); !errors.Is(err, ErrPageNotInPool) {
		t.Fatalf("evict non-resident: want ErrPageNotInPool, got %v", err)
	}
}

func TestViewDoesNotChangeState(t *testing.T) {
	cb := mustNew(t, 1024, 5, 1_000_000_000_000)
	cb.Op(1, Insert, "a", 100)
	cb.Op(1, DeleteMark, "a", 0)
	cb.Op(1, Insert, "b", 20)
	before := snapshot(cb)
	v1, _ := cb.View(1)
	v2, _ := cb.View(1)
	after := snapshot(cb)
	p0, p1 := before[1], after[1]
	if p1.inPool != p0.inPool || p1.used != p0.used ||
		p1.bufBytes != p0.bufBytes || len(p1.queue) != len(p0.queue) {
		t.Fatalf("View changed state")
	}
	for i := range v1 {
		if v1[i] != v2[i] {
			t.Fatalf("View not reproducible")
		}
	}
}

func TestInvalidArguments(t *testing.T) {
	bad := [][3]int64{
		{63, 1, 1}, {1_000_001, 1, 1},
		{64, 0, 1}, {64, 1_000_001, 1},
		{64, 1, 0}, {64, 1, 1_000_000_000_001},
	}
	for _, a := range bad {
		if _, err := New(a[0], a[1], a[2]); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("New(%v): want ErrInvalidArgument, got %v", a, err)
		}
	}
	cb := mustNew(t, 1024, 10, 1000)
	if _, err := cb.Op(-1, Insert, "k", 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("page<0: %v", err)
	}
	if _, err := cb.Op(MaxPage+1, Insert, "k", 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("page>Max: %v", err)
	}
	if _, err := cb.Op(1, Kind(0), "k", 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("bad kind: %v", err)
	}
	if _, err := cb.Op(1, Insert, "", 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty key: %v", err)
	}
	if _, err := cb.Op(1, Insert, "k", 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("insert e=0: %v", err)
	}
	if _, err := cb.Op(1, Insert, "k", 1025); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("insert e>S: %v", err)
	}
	if _, err := cb.Op(1, DeleteMark, "k", 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("mark e=1: %v", err)
	}
	if _, err := cb.Op(1, Purge, "k", 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("purge e=1: %v", err)
	}
	if err := cb.Load(-1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("load bad page: %v", err)
	}
	if err := cb.Evict(-1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("evict bad page: %v", err)
	}
}

func TestErrorOrderingEvictBeforeSpace(t *testing.T) {
	// Per the reporting order, Evict's not-in-pool error precedes Op space
	// errors; verify each sentinel surfaces for its own call.
	cb := mustNew(t, 100, 10, 1_000_000_000_000)
	if err := cb.Evict(3); !errors.Is(err, ErrPageNotInPool) {
		t.Fatalf("evict: %v", err)
	}
	// An invalid argument to Op is still reported regardless of residency.
	if err := cb.Evict(MaxPage + 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("evict invalid page: %v", err)
	}
}
