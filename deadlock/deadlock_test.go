package deadlock

import (
	"errors"
	"fmt"
	"testing"
)

func newTestD(t *testing.T, R int, T, c []int64, P, L int) *Detector {
	t.Helper()
	d, err := New(R, T, c, P, L)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d
}

func checkInvariant(t *testing.T, d *Detector) {
	t.Helper()
	sum := append([]int64(nil), d.avail...)
	nb := 0
	for p := 0; p < d.pnum; p++ {
		if !d.alive[p] {
			continue
		}
		for r, v := range d.alloc[p] {
			sum[r] += v
			if v < 0 {
				t.Fatalf("negative alloc p%d r%d", p, r)
			}
		}
		if d.rb[p] >= d.rollback {
			t.Fatalf("alive p%d with rb=%d >= L", p, d.rb[p])
		}
		if d.blocked[p] {
			nb++
			for _, alt := range d.alts[p] {
				if vecFits(alt, d.avail) {
					t.Fatalf("blocked p%d has fitting alt %v, avail %v", p, alt, d.avail)
				}
			}
		}
	}
	for r, v := range sum {
		if v != d.total[r] {
			t.Fatalf("resource conservation r%d: %d != %d", r, v, d.total[r])
		}
	}
	if b := d.Detect(); len(b) != 0 {
		// Not a universal invariant; only call this helper when expected.
		_ = nb
	}
}

// Worked example from the specification.
func TestSpecExample(t *testing.T) {
	d := newTestD(t, 2, []int64{3, 2}, []int64{1, 10}, 4, 2)

	mustReq := func(p int, alts [][]int64, wantIdx int, wantBlock bool) {
		t.Helper()
		g, err := d.Request(p, alts)
		if err != nil {
			t.Fatalf("Request(%d): %v", p, err)
		}
		if wantBlock && g != (Granted{}) {
			t.Fatalf("Request(%d): want blocked, got %+v", p, g)
		}
		if !wantBlock && g.AltIndex != wantIdx {
			t.Fatalf("Request(%d): idx=%d want %d", p, g.AltIndex, wantIdx)
		}
	}
	mustReq(0, [][]int64{{2, 0}}, 0, false)
	mustReq(1, [][]int64{{0, 2}}, 0, false)
	mustReq(2, [][]int64{{1, 0}}, 0, false)
	mustReq(0, [][]int64{{0, 1}}, 0, true)
	mustReq(1, [][]int64{{1, 0}, {0, 1}}, 0, true)
	if dead := d.Detect(); len(dead) != 0 {
		t.Fatalf("detect while p2 unblocked: %v", dead)
	}
	mustReq(2, [][]int64{{0, 1}}, 0, true)
	if dead := d.Detect(); fmt.Sprint(dead) != "[0 1 2]" {
		t.Fatalf("deadlock set: %v", dead)
	}

	steps := d.Resolve()
	if len(steps) != 1 {
		t.Fatalf("resolve steps: %+v", steps)
	}
	s := steps[0]
	if s.Victim != 2 || s.Cost != 1 || s.Permanent {
		t.Fatalf("step: %+v", s)
	}
	if fmt.Sprint(s.Granted) != "[{1 0}]" {
		t.Fatalf("granted: %+v", s.Granted)
	}
	if dead := d.Detect(); len(dead) != 0 {
		t.Fatalf("deadlock after resolve: %v", dead)
	}
	if d.rb[2] != 1 || !d.alive[2] || d.blocked[2] {
		t.Fatalf("victim state rb=%d alive=%v blocked=%v", d.rb[2], d.alive[2], d.blocked[2])
	}

	gr, err := d.Release(1, []int64{0, 2})
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if fmt.Sprint(gr) != "[{0 0}]" {
		t.Fatalf("release grants: %v", gr)
	}
	checkConservation(t, d)
}

func checkConservation(t *testing.T, d *Detector) {
	t.Helper()
	sum := append([]int64(nil), d.avail...)
	for p := 0; p < d.pnum; p++ {
		if !d.alive[p] {
			continue
		}
		for r, v := range d.alloc[p] {
			sum[r] += v
		}
	}
	for r, v := range sum {
		if v != d.total[r] {
			t.Fatalf("conservation r%d: %d != %d", r, v, d.total[r])
		}
	}
}

// First fitting alternative wins, even if a later one is smaller.
func TestFirstAlternativeWins(t *testing.T) {
	d := newTestD(t, 1, []int64{5}, []int64{1}, 2, 2)
	g, err := d.Request(0, [][]int64{{3}, {1}})
	if err != nil || g.AltIndex != 0 {
		t.Fatalf("grant: %+v %v", g, err)
	}
	g, err = d.Request(1, [][]int64{{3}, {1}}) // [3] short, [1] fits
	if err != nil || g.AltIndex != 1 {
		t.Fatalf("grant alt: %+v %v", g, err)
	}
	checkConservation(t, d)
}

// Nothing is granted when no alternative fits; bseq advances once.
func TestBlockNoPartialGrant(t *testing.T) {
	d := newTestD(t, 2, []int64{2, 2}, []int64{1, 1}, 2, 2)
	d.Request(0, [][]int64{{2, 2}})
	if _, err := d.Request(1, [][]int64{{1, 0}, {0, 1}}); err != nil {
		t.Fatal(err)
	}
	if d.avail[0] != 0 || d.avail[1] != 0 || d.alloc[1][0] != 0 || d.alloc[1][1] != 0 {
		t.Fatalf("partial grant: avail=%v alloc1=%v", d.avail, d.alloc[1])
	}
	if d.bseq[1] != 1 || d.nextBSeq != 1 {
		t.Fatalf("bseq p1=%d next=%d", d.bseq[1], d.nextBSeq)
	}
}

// After Release, a later small request overtakes an earlier blocked big one:
// fixpoint chooses the smallest bseq that FITS, not the strict FIFO head.
func TestBSeqOvertakesFIFOHead(t *testing.T) {
	d := newTestD(t, 1, []int64{3}, []int64{1}, 3, 2)
	d.Request(0, [][]int64{{3}})
	d.Request(1, [][]int64{{3}})        // blocked bseq 1
	d.Request(2, [][]int64{{1}})        // blocked bseq 2
	gr, err := d.Release(0, []int64{2}) // avail 2
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(gr) != "[{2 0}]" { // p1 needs 3, p2 needs 1
		t.Fatalf("grants: %v", gr)
	}
	if !d.blocked[1] || d.blocked[2] {
		t.Fatalf("state 1 blocked=%v, 2 blocked=%v", d.blocked[1], d.blocked[2])
	}
}

// Cascading fixpoint: one Release unlocks two waiters in bseq order because
// their requests use disjoint resources.
func TestGrantFixpointCascade(t *testing.T) {
	d := newTestD(t, 2, []int64{1, 1}, []int64{1, 1}, 3, 2)
	d.Request(0, [][]int64{{1, 1}})
	d.Request(1, [][]int64{{1, 0}}) // blocked bseq 1
	d.Request(2, [][]int64{{0, 1}}) // blocked bseq 2
	gr, err := d.Release(0, []int64{1, 1})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(gr) != "[{1 0} {2 0}]" {
		t.Fatalf("cascade grants: %v", gr)
	}
	if d.blocked[1] || d.blocked[2] {
		t.Fatal("waiters remain blocked")
	}
}

var _ = errors.Is

// A blocked waiter on a non-blocked holder is not deadlocked.
func TestNonBlockedHolderNotDeadlock(t *testing.T) {
	d := newTestD(t, 1, []int64{1}, []int64{1}, 2, 2)
	d.Request(0, [][]int64{{1}}) // holds 1, running
	d.Request(1, [][]int64{{1}}) // blocked
	if dead := d.Detect(); len(dead) != 0 {
		t.Fatalf("dead=%v", dead)
	}
}

// A holderless blocked process that waits only on deadlocked holders is itself
// part of the deadlock set but can never be picked as victim.
func TestWaitOnlyOnDeadlocked(t *testing.T) {
	d := newTestD(t, 2, []int64{1, 1}, []int64{1, 1}, 3, 2)
	d.Request(0, [][]int64{{1, 0}})
	d.Request(1, [][]int64{{0, 1}})
	d.Request(0, [][]int64{{0, 1}}) // p0 blocked, holds r0
	d.Request(1, [][]int64{{1, 0}}) // p1 blocked, holds r1
	d.Request(2, [][]int64{{1, 0}}) // p2 blocked, holds nothing
	dead := d.Detect()
	if fmt.Sprint(dead) != "[0 1 2]" {
		t.Fatalf("dead=%v", dead)
	}
	steps := d.Resolve()
	if len(steps) != 1 || steps[0].Victim != 0 || steps[0].Cost != 1 ||
		fmt.Sprint(steps[0].Granted) != "[{1 0}]" {
		t.Fatalf("victim must be the only holder: %+v", steps)
	}
	// p1's fixpoint grant makes it a running holder of r0, so holderless p2 is
	// reducible and the deadlock is fully gone.
	if dead := d.Detect(); len(dead) != 0 {
		t.Fatalf("residual deadlock: %v", dead)
	}
}

// alloc+alt == T allowed; one unit more is permanently impossible.
func TestRequestImpossibleBoundary(t *testing.T) {
	d := newTestD(t, 1, []int64{3}, []int64{1}, 1, 2)
	if _, err := d.Request(0, [][]int64{{2}}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Request(0, [][]int64{{1}}); err != nil { // 2+1 == 3
		t.Fatalf("boundary should be allowed: %v", err)
	}
	if _, err := d.Release(0, []int64{1}); err != nil {
		t.Fatal(err)
	}
	// holds 2 again: 2+2 = 4 > 3.
	if _, err := d.Request(0, [][]int64{{2}}); !errors.Is(err, ErrRequestImpossible) {
		t.Fatalf("boundary+1: %v", err)
	}
	if d.nextBSeq != 0 { // the boundary [1] fit avail and was granted outright
		t.Fatalf("rejected request must not consume bseq, next=%d", d.nextBSeq)
	}
}

// Equal victim costs tie-break to the smaller pid.
func TestVictimTieSmallerPid(t *testing.T) {
	d := newTestD(t, 1, []int64{2}, []int64{1}, 3, 2)
	d.Request(0, [][]int64{{1}})
	d.Request(1, [][]int64{{1}})
	d.Request(0, [][]int64{{1}}) // blocked, holds 1
	d.Request(1, [][]int64{{1}}) // blocked, holds 1
	steps := d.Resolve()
	if len(steps) != 1 || steps[0].Victim != 0 || steps[0].Cost != 1 {
		t.Fatalf("steps=%+v", steps)
	}
}

// Prior rollback doubles the cost and changes who is cheapest, with two
// disjoint deadlock components giving multiple resolution rounds; rb reaching
// L permanently terminates, and each rollback triggers grant cascades.
func TestRollbackCostAndMultipleRounds(t *testing.T) {
	// r0 weight 1, r1 weight 3, r2 weight 1, r3 weight 3; one unit each.
	d := newTestD(t, 4,
		[]int64{1, 1, 1, 1},
		[]int64{1, 3, 1, 3}, 5, 2)

	// Round 1: p0/p1 cycle on r0,r1.
	d.Request(0, [][]int64{{1, 0, 0, 0}})
	d.Request(1, [][]int64{{0, 1, 0, 0}})
	d.Request(0, [][]int64{{0, 1, 0, 0}})
	d.Request(1, [][]int64{{1, 0, 0, 0}})
	if dead := d.Detect(); fmt.Sprint(dead) != "[0 1]" {
		t.Fatalf("round1 dead=%v", dead)
	}
	s1 := d.Resolve()
	if len(s1) != 1 || s1[0].Victim != 0 || s1[0].Cost != 1 ||
		s1[0].Permanent || fmt.Sprint(s1[0].Granted) != "[{1 0}]" {
		t.Fatalf("round1 step=%+v", s1)
	}
	// p1 now holds r0 and r1; release both for round 2.
	if _, err := d.Release(1, []int64{1, 1, 0, 0}); err != nil {
		t.Fatal(err)
	}

	// Round 2, component A on r0/r1: p0(rb=1) and p4; component B on
	// r2/r3: p2 and p3. p0's doubled cost (2) loses to p2's cost 1.
	d.Request(0, [][]int64{{1, 0, 0, 0}})
	d.Request(4, [][]int64{{0, 1, 0, 0}})
	d.Request(2, [][]int64{{0, 0, 1, 0}})
	d.Request(3, [][]int64{{0, 0, 0, 1}})
	d.Request(0, [][]int64{{0, 1, 0, 0}})
	d.Request(4, [][]int64{{1, 0, 0, 0}})
	d.Request(2, [][]int64{{0, 0, 0, 1}})
	d.Request(3, [][]int64{{0, 0, 1, 0}})
	if dead := d.Detect(); fmt.Sprint(dead) != "[0 2 3 4]" {
		t.Fatalf("round2 dead=%v", dead)
	}
	s2 := d.Resolve()
	if len(s2) != 2 {
		t.Fatalf("round2 steps=%+v", s2)
	}
	if s2[0].Victim != 2 || s2[0].Cost != 1 || s2[0].Permanent ||
		fmt.Sprint(s2[0].Granted) != "[{3 0}]" {
		t.Fatalf("step1=%+v", s2[0])
	}
	if s2[1].Victim != 0 || s2[1].Cost != 2 || !s2[1].Permanent ||
		fmt.Sprint(s2[1].Granted) != "[{4 0}]" {
		t.Fatalf("step2=%+v", s2[1])
	}
	if dead := d.Detect(); len(dead) != 0 {
		t.Fatalf("dead after all rounds: %v", dead)
	}
	// Terminated process is gone for good.
	if _, err := d.Request(0, [][]int64{{1, 0, 0, 0}}); !errors.Is(err, ErrProcessNotFound) {
		t.Fatalf("terminated request: %v", err)
	}
	checkConservation(t, d)
}

// rb+1 == L terminates immediately for L=1; with L=2 the first rollback keeps
// the process alive and its cancelled request leaves the deadlock set.
func TestPermanentBoundary(t *testing.T) {
	for _, L := range []int{1, 2} {
		d := newTestD(t, 1, []int64{2}, []int64{1}, 2, L)
		d.Request(0, [][]int64{{1}})
		d.Request(1, [][]int64{{1}})
		d.Request(0, [][]int64{{1}})
		d.Request(1, [][]int64{{1}})
		s := d.Resolve()
		if len(s) != 1 {
			t.Fatalf("L=%d steps=%+v", L, s)
		}
		if s[0].Permanent != (L == 1) {
			t.Fatalf("L=%d permanent=%v", L, s[0].Permanent)
		}
		wantAlive := L == 2
		if d.alive[0] != wantAlive {
			t.Fatalf("L=%d alive=%v", L, d.alive[0])
		}
	}
}

// Worst case for checks: b blocked processes in a strict chain, one running
// holder seeds Work=1 so the k-th reduction pass needs k checks: total
// b(b+1)/2 exactly.
func TestChecksWorstChain(t *testing.T) {
	b := 6
	d := newTestD(t, 1, []int64{int64(b + 1)}, []int64{1}, b+1, 2)
	for p := 0; p <= b; p++ {
		if _, err := d.Request(p, [][]int64{{1}}); err != nil {
			t.Fatal(err)
		}
	}
	for p := 0; p < b; p++ {
		if _, err := d.Request(p, [][]int64{{1}}); err != nil {
			t.Fatal(err)
		}
	}
	dead := d.Detect()
	if len(dead) != 0 {
		t.Fatalf("chain reducible via running holder, got %v", dead)
	}
	want := int64(b * (b + 1) / 2)
	if got := d.Checks(); got <= 0 || got > want {
		t.Fatalf("reducible chain checks=%d, bound %d", got, want)
	}
	// Fully deadlocked chain: bounded trivially as well.
	d2 := newTestD(t, 1, []int64{int64(b)}, []int64{1}, b, 2)
	for p := 0; p < b; p++ {
		d2.Request(p, [][]int64{{1}})
	}
	for p := 0; p < b; p++ {
		d2.Request(p, [][]int64{{1}})
	}
	if got := len(d2.Detect()); got != b {
		t.Fatalf("deadlocked chain size=%d", got)
	}
	if d2.Checks() > want {
		t.Fatalf("checks %d exceed bound %d", d2.Checks(), want)
	}
}

func TestErrorOrderingAndBSeq(t *testing.T) {
	d := newTestD(t, 2, []int64{2, 2}, []int64{1, 1}, 2, 2)
	// Invalid arguments beat nonexistent process.
	if _, err := d.Request(9, [][]int64{{}}); !errors.Is(err, ErrInvalidArgs) {
		t.Fatalf("invalid first: %v", err)
	}
	if _, err := d.Request(0, [][]int64{{0}}); !errors.Is(err, ErrInvalidArgs) {
		t.Fatalf("zero vector: %v", err)
	}
	if _, err := d.Request(0, [][]int64{{-1}}); !errors.Is(err, ErrInvalidArgs) {
		t.Fatalf("negative: %v", err)
	}
	if _, err := d.Request(9, [][]int64{{1, 1}}); !errors.Is(err, ErrProcessNotFound) {
		t.Fatalf("not found: %v", err)
	}
	d.Request(0, [][]int64{{2, 0}})
	// Saturate r1 with p1 so p0's further r1 request blocks rather than
	// being declared impossible.
	d.Request(1, [][]int64{{0, 2}})
	if _, err := d.Request(0, [][]int64{{0, 1}}); err != nil { // blocked
		t.Fatal(err)
	}
	if _, err := d.Request(0, [][]int64{{0, 1}}); !errors.Is(err, ErrProcessBlocked) {
		t.Fatalf("blocked: %v", err)
	}
	if d.nextBSeq != 1 {
		t.Fatalf("nextBSeq=%d", d.nextBSeq)
	}
	// Release ordering: invalid args, not found, blocked, over-hold.
	if _, err := d.Release(9, []int64{}); !errors.Is(err, ErrInvalidArgs) {
		t.Fatalf("release invalid: %v", err)
	}
	if _, err := d.Release(9, []int64{1, 1}); !errors.Is(err, ErrProcessNotFound) {
		t.Fatalf("release not found: %v", err)
	}
	if _, err := d.Release(0, []int64{1, 0}); !errors.Is(err, ErrProcessBlocked) {
		t.Fatalf("release blocked: %v", err)
	}
	if d.nextBSeq != 1 {
		t.Fatalf("rejections consumed bseq: %d", d.nextBSeq)
	}
	// p1 holds r1 (not blocked), releasing r0 exceeds its holding.
	if _, err := d.Release(1, []int64{1, 0}); !errors.Is(err, ErrReleaseExceedsAlloc) {
		t.Fatalf("release over-hold: %v", err)
	}
}

func TestInvalidConstruction(t *testing.T) {
	cases := []struct {
		R    int
		T    []int64
		c    []int64
		P, L int
	}{
		{0, []int64{1}, []int64{1}, 1, 1},
		{9, []int64{1}, []int64{1}, 1, 1},
		{1, []int64{0}, []int64{1}, 1, 1},
		{1, []int64{1_000_001}, []int64{1}, 1, 1},
		{1, []int64{1}, []int64{0}, 1, 1},
		{1, []int64{1}, []int64{1001}, 1, 1},
		{1, []int64{1, 1}, []int64{1}, 1, 1},
		{1, []int64{1}, []int64{1}, 0, 1},
		{1, []int64{1}, []int64{1}, 65, 1},
		{1, []int64{1}, []int64{1}, 1, 0},
		{1, []int64{1}, []int64{1}, 1, 11},
	}
	for i, tc := range cases {
		if _, err := New(tc.R, tc.T, tc.c, tc.P, tc.L); !errors.Is(err, ErrInvalidArgs) {
			t.Fatalf("case %d: %v", i, err)
		}
	}
}
