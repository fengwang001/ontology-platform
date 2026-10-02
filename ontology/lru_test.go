package ontology

import (
	"errors"
	"testing"
)

func mustNew(t *testing.T, n, rho, tol int, T int64) *Pool {
	t.Helper()
	p, err := New(n, rho, tol, T)
	if err != nil {
		t.Fatalf("New(%d,%d,%d,%d): %v", n, rho, tol, T, err)
	}
	return p
}

func eq(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func in(x int, xs []int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// Worked example from the specification.
func TestSpecExample(t *testing.T) {
	p := mustNew(t, 8, 50, 0, 10)
	checks := []struct {
		page int
		now  int64
		y    []int
		o    []int
	}{
		{0, 0, []int{0}, []int{}},
		{1, 1, []int{0}, []int{1}},
		{2, 2, []int{0, 2}, []int{1}},
		{3, 3, []int{0, 2}, []int{3, 1}},
	}
	for _, c := range checks {
		if _, err := p.Access(c.page, c.now); err != nil {
			t.Fatalf("Access %d: %v", c.page, err)
		}
		gotY, gotO := p.Lists()
		if !eq(gotY, c.y) || !eq(gotO, c.o) {
			t.Fatalf("after page %d: Y=%v O=%v, want Y=%v O=%v", c.page, gotY, gotO, c.y, c.o)
		}
	}
	if _, err := p.Access(1, 5); err != nil { // 5-1=4 < 10
		t.Fatal(err)
	}
	if y, o := p.Lists(); !eq(y, []int{0, 2}) || !eq(o, []int{3, 1}) {
		t.Fatalf("B@5: Y=%v O=%v", y, o)
	}
	if _, err := p.Access(1, 11); err != nil { // 11-1=10 >= 10
		t.Fatal(err)
	}
	if y, o := p.Lists(); !eq(y, []int{1, 0}) || !eq(o, []int{2, 3}) {
		t.Fatalf("B@11: Y=%v O=%v", y, o)
	}
	if _, err := p.Prefetch(4, 12); err != nil {
		t.Fatal(err)
	}
	if y, o := p.Lists(); !eq(y, []int{1, 0, 4}) || !eq(o, []int{2, 3}) {
		t.Fatalf("prefetch E: Y=%v O=%v", y, o)
	}
	if e := p.index[4]; e.hasF {
		t.Fatalf("prefetched E must keep empty first, got %d", e.first)
	}
}

// Promotion fires exactly at now-first == T; one less does not.
func TestPromotionBoundary(t *testing.T) {
	p := mustNew(t, 8, 50, 0, 10)
	for i := 0; i < 4; i++ {
		p.Access(i, int64(i))
	}
	p.Access(1, 10) // 10-1=9 < 10
	if y, o := p.Lists(); !eq(y, []int{0, 2}) || !eq(o, []int{3, 1}) {
		t.Fatalf("delta 9 must not promote: Y=%v O=%v", y, o)
	}
	p.Access(1, 11) // delta 10: promotes
	if _, o := p.Lists(); in(1, o) {
		t.Fatalf("delta 10 must promote page 1, O=%v", o)
	}
}

// T == 0: an old page promotes on the very first hit after its first access.
func TestTZeroPromotesImmediately(t *testing.T) {
	p := mustNew(t, 8, 25, 1, 0)
	p.Access(7, 0) // len1 tgt0 tol1: O may hold one page
	if _, o := p.Lists(); !in(7, o) {
		t.Fatalf("7 should be in O: O=%v", o)
	}
	p.Access(7, 0) // same timestamp; 0-0 >= 0 -> promote
	if y, _ := p.Lists(); !in(7, y) {
		t.Fatalf("T=0 old hit must promote even at same now, Y=%v", y)
	}
}

// First hit on a prefetched page only records first; no promotion.
func TestPrefetchFirstHitRecordsFirstOnly(t *testing.T) {
	p := mustNew(t, 8, 50, 2, 100)
	p.Access(0, 0)
	p.Access(1, 1)
	p.Prefetch(2, 5) // in O, first empty
	if e := p.index[2]; e.hasF {
		t.Fatal("prefetch must not set first")
	}
	p.Access(2, 7) // records first=7, must not move
	e := p.index[2]
	if !e.hasF || e.first != 7 {
		t.Fatalf("first should be 7, got hasF=%v first=%d", e.hasF, e.first)
	}
	if _, o := p.Lists(); !in(2, o) {
		t.Fatalf("first hit on prefetched page must remain in O, O=%v", o)
	}
	p.Access(2, 106) // 106-7=99 < 100
	if _, o := p.Lists(); !in(2, o) {
		t.Fatalf("delta 99 must not promote, O=%v", o)
	}
	p.Access(2, 107) // delta 100
	if y, _ := p.Lists(); !in(2, y) {
		t.Fatalf("delta 100 must promote, Y=%v", y)
	}
}

// A hit in young never changes first (the value survives Rebalance and
// eviction is the only thing that clears it).
func TestYoungHitDoesNotChangeFirst(t *testing.T) {
	p := mustNew(t, 8, 50, 0, 10)
	p.Access(0, 0)
	p.Access(1, 1)
	p.Access(2, 2) // Y=[0,2]; page 2 carries first=2 from its fault
	if e := p.index[2]; !e.hasF || e.first != 2 {
		t.Fatalf("setup: page 2 first=%v,%d", e.hasF, e.first)
	}
	p.Access(2, 99) // hit in Y
	if e := p.index[2]; !e.hasF || e.first != 2 {
		t.Fatalf("young hit must keep first=2, got %v,%d", e.hasF, e.first)
	}
	if y, _ := p.Lists(); len(y) == 0 || y[0] != 2 {
		t.Fatalf("young hit must move to Y head, Y=%v", y)
	}
}

// A fault's new page gets first = that Access's now.
func TestMissFirstIsNow(t *testing.T) {
	p := mustNew(t, 8, 50, 2, 10)
	p.Prefetch(9, 0) // empty first
	p.Prefetch(8, 1)
	p.Access(7, 42) // fault: first must be 42
	if e := p.index[7]; !e.hasF || e.first != 42 {
		t.Fatalf("new fault first = hasF:%v first:%d", e.hasF, e.first)
	}
	p.Access(7, 51) // 9 < 10
	if _, o := p.Lists(); !in(7, o) {
		t.Fatal("should not promote yet")
	}
	p.Access(7, 52) // delta 10: promote
	if y, _ := p.Lists(); !in(7, y) {
		t.Fatal("should promote from now=42 baseline")
	}
}

// tol > 0 keeps freshly inserted pages in old: O may overshoot tgt by Tol.
func TestToleranceKeepsNewInOld(t *testing.T) {
	p := mustNew(t, 8, 50, 1, 10)
	for i, now := 0, int64(0); i < 4; i, now = i+1, now+1 {
		p.Access(i, now)
	}
	// rho50 tol1: tgt=floor(L/2). L=1 tgt0 O<=1; L=2 tgt1; L=3 tgt1 O<=2;
	// L=4 tgt2. Final: Y=[2] O=[3,1,0].
	if y, o := p.Lists(); !eq(y, []int{2}) || !eq(o, []int{3, 1, 0}) {
		t.Fatalf("tol=1 layout Y=%v O=%v", y, o)
	}
}

// After promotion, rebalance moves the young tail back to the old head.
func TestPromotionRebalanceMovesYoungTail(t *testing.T) {
	p := mustNew(t, 8, 50, 0, 10)
	for i := 0; i < 4; i++ {
		p.Access(i, int64(i))
	}
	p.Access(1, 5)  // no promote
	p.Access(1, 11) // promote; Y tail (2) moves to O head
	if y, o := p.Lists(); !eq(y, []int{1, 0}) || !eq(o, []int{2, 3}) {
		t.Fatalf("Y=%v O=%v", y, o)
	}
}

// Full pool: victim from old tail skipping pinned pages; when all of old is
// pinned the search falls back to the young tail.
func TestEvictionOrderAndPinning(t *testing.T) {
	p := mustNew(t, 4, 50, 0, 100)
	for i := 0; i < 4; i++ {
		p.Access(i, int64(i))
	}
	// N=4 rho50 tol0: len4 tgt2 => Y=[0,2] O=[3,1]
	if y, o := p.Lists(); !eq(y, []int{0, 2}) || !eq(o, []int{3, 1}) {
		t.Fatalf("setup Y=%v O=%v", y, o)
	}
	// Pin old-tail 3: scan skips it and evicts 1.
	if err := p.Pin(3); err != nil {
		t.Fatal(err)
	}
	res, err := p.Access(4, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Evicted || res.Page != 1 {
		t.Fatalf("want evict page 1, got %+v", res)
	}
	if _, ok := p.index[1]; ok {
		t.Fatal("evicted page must be removed")
	}
	if y, o := p.Lists(); !eq(y, []int{0, 2}) || !eq(o, []int{4, 3}) {
		t.Fatalf("after fault Y=%v O=%v", y, o)
	}
	if ev := p.Evictions(); !eq(ev, []int{1}) {
		t.Fatalf("eviction log %v", ev)
	}

	// All of O pinned (4 and 3): victim search falls through to young tail (2).
	p.Pin(4)
	p.Pin(3)
	res, err = p.Access(5, 11)
	if err != nil || !res.Evicted || res.Page != 2 {
		t.Fatalf("want young-tail 2 evicted, got %+v %v", res, err)
	}
	// Insert 5 at O head: O=[5,4,3] (tgt2), rebalance moves O head 5 to Y
	// tail: Y=[0,5], O=[4,3].
	if y, o := p.Lists(); !eq(y, []int{0, 5}) || !eq(o, []int{4, 3}) {
		t.Fatalf("after second fault Y=%v O=%v", y, o)
	}
}

// Full pool with every page pinned: rejection; clock and state unchanged.
func TestAllPinnedRejects(t *testing.T) {
	p := mustNew(t, 4, 50, 0, 100)
	for i := 0; i < 4; i++ {
		p.Access(i, 0)
	}
	for i := 0; i < 4; i++ {
		p.Pin(i)
	}
	beforeY, beforeO := p.Lists()
	beforeNow := p.now
	res, err := p.Access(99, 50)
	if !errors.Is(err, ErrAllPinned) {
		t.Fatalf("want ErrAllPinned, got %v", err)
	}
	if res.Evicted || p.Len() != 4 {
		t.Fatalf("state changed: %+v len=%d", res, p.Len())
	}
	if p.now != beforeNow {
		t.Fatalf("clock advanced on rejection: %d -> %d", beforeNow, p.now)
	}
	if y, o := p.Lists(); !eq(y, beforeY) || !eq(o, beforeO) {
		t.Fatalf("lists changed: Y=%v O=%v (was Y=%v O=%v)", y, o, beforeY, beforeO)
	}
	for i := 0; i < 4; i++ {
		p.Unpin(i)
	}
	if _, err := p.Access(99, 50); err != nil {
		t.Fatalf("after unpin: %v", err)
	}
}

// Unpin underflow is rejected and leaves the counter unchanged.
func TestUnpinUnderflow(t *testing.T) {
	p := mustNew(t, 8, 50, 0, 10)
	p.Access(1, 0)
	if err := p.Unpin(1); !errors.Is(err, ErrPinUnderflow) {
		t.Fatalf("want ErrPinUnderflow, got %v", err)
	}
	if e := p.index[1]; e.pin != 0 {
		t.Fatal("pin changed on underflow")
	}
	p.Pin(1)
	p.Unpin(1)
	if err := p.Unpin(1); !errors.Is(err, ErrPinUnderflow) {
		t.Fatalf("second underflow: %v", err)
	}
	if err := p.Unpin(7); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing page: %v", err)
	}
}

// Prefetch of a resident page only advances the clock, changing nothing else.
func TestPrefetchResidentAdvancesClock(t *testing.T) {
	p := mustNew(t, 8, 50, 0, 10)
	p.Access(0, 0)
	beforeY, beforeO := p.Lists()
	res, err := p.Prefetch(0, 30)
	if err != nil || !res.Hit || res.Evicted {
		t.Fatalf("resident prefetch: %+v %v", res, err)
	}
	if y, o := p.Lists(); !eq(y, beforeY) || !eq(o, beforeO) {
		t.Fatalf("resident prefetch reordered lists: Y=%v O=%v", y, o)
	}
	// Same now stays valid; a lower now is clock skew.
	if _, err := p.Access(0, 30); err != nil {
		t.Fatalf("equal now must be accepted: %v", err)
	}
	if _, err := p.Access(0, 29); !errors.Is(err, ErrClockSkew) {
		t.Fatalf("want ErrClockSkew, got %v", err)
	}
	// Rejected clock-skew op must not move the page.
	if y, _ := p.Lists(); !eq(y, beforeY) {
		t.Fatalf("clock-skew rejection changed lists: %v", y)
	}
}

// Rejected operations change nothing: argument validation wins over skew,
// and skew wins over not-found / all-pinned.
func TestRejectionPriorityAndAtomicity(t *testing.T) {
	p := mustNew(t, 4, 50, 0, 10)
	p.Access(0, 100)
	// Bad argument reported even with clock skew.
	if _, err := p.Access(1_000_001, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("bad page: %v", err)
	}
	if _, err := p.Access(0, maxNow+1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("bad now: %v", err)
	}
	// Clock skew reported before pool-state errors.
	if err := p.Pin(0); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Access(7, 50); !errors.Is(err, ErrClockSkew) {
		t.Fatalf("skew should win over all-pinned, got %v", err)
	}
	if err := p.Unpin(7); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Pin/Unpin have no clock: %v", err)
	}
}

func TestInvalidConstruction(t *testing.T) {
	bad := [][4]int64{
		{3, 50, 0, 10}, {1_000_001, 50, 0, 10},
		{8, 4, 0, 10}, {8, 96, 0, 10},
		{8, 50, -1, 10}, {8, 50, 9, 10},
		{8, 50, 0, -1}, {8, 50, 0, 1_000_000_001},
	}
	for _, b := range bad {
		if _, err := New(int(b[0]), int(b[1]), int(b[2]), b[3]); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("New(%v): want ErrInvalidArgument, got %v", b, err)
		}
	}
}
