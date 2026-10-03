package hotrank

import (
	"errors"
	"testing"
)

// b == cur-W is expired; b == cur-W+1 still accepted as late data.
func TestLateArrivalBoundary(t *testing.T) {
	b, _ := New(10, 3, 1, 100, 1)
	if err := b.Add("x", 5, 35); err != nil { // c=35, cur=3
		t.Fatal(err)
	}
	if err := b.Add("y", 5, 0); err != ErrExpired { // b=0 <= 3-3
		t.Fatalf("expired boundary got %v", err)
	}
	if err := b.Add("y", 5, 10); err != nil { // b=1 == cur-W+1
		t.Fatalf("late add should be accepted: %v", err)
	}
	if got := b.Score("y"); got != 5 {
		t.Fatalf("y score = %d", got)
	}
}

// Bucket boundaries t=iL-1 and t=iL land in adjacent buckets.
func TestBucketBoundaries(t *testing.T) {
	b, _ := New(10, 100, 1, 100, 1)
	if err := b.Add("a", 7, 9); err != nil {
		t.Fatal(err)
	}
	if err := b.Add("a", 11, 10); err != nil {
		t.Fatal(err)
	}
	if got := b.Score("a"); got != 18 {
		t.Fatalf("score = %d", got)
	}
	r, err := b.Snapshot(1009) // cur=100, bucket 0 exits
	if err != nil {
		t.Fatal(err)
	}
	if r.Board[0].Score != 11 {
		t.Fatalf("after bucket exit score=%d", r.Board[0].Score)
	}
}

// One event advancing c pushes old buckets out of the window.
func TestAddAdvanceEvicts(t *testing.T) {
	b, _ := New(10, 2, 1, 100, 1)
	if err := b.Add("a", 5, 0); err != nil {
		t.Fatal(err)
	}
	if err := b.Add("a", 7, 30); err != nil { // cur 0->3, bucket 0 exits
		t.Fatal(err)
	}
	if got := b.Score("a"); got != 7 {
		t.Fatalf("score after jump = %d", got)
	}
}

// M - floor(M/4) rounding for M in {1,3,4,100}: 1,3,3,75.
func TestHysteresisRounding(t *testing.T) {
	cases := []struct{ m, low int64 }{
		{1, 1}, {3, 3}, {4, 3}, {100, 75},
	}
	for _, tc := range cases {
		b, _ := New(10, 10, tc.m, 10, 2)
		if got := b.hysteresisLine(); got != tc.low {
			t.Fatalf("M=%d low=%d want %d", tc.m, got, tc.low)
		}
	}
}

// Dense ranks 1,2,2 and a tied fourth candidate beyond the K cut.
func TestTieRanksAndKCut(t *testing.T) {
	b, _ := New(10, 10, 1, 3, 1)
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(b.Add("a", 5, 1))
	must(b.Add("b", 4, 1))
	must(b.Add("c", 4, 1))
	must(b.Add("d", 4, 1))
	r, err := b.Snapshot(5)
	must(err)
	want := []RankItem{
		{ID: "a", Score: 5, Rank: 1, New: true},
		{ID: "b", Score: 4, Rank: 2, New: true},
		{ID: "c", Score: 4, Rank: 2, New: true},
	}
	for i := range want {
		if r.Board[i] != want[i] {
			t.Fatalf("board[%d]=%+v want %+v", i, r.Board[i], want[i])
		}
	}
}

// A previous-board item cut by K among ties is reported dropped.
func TestKCutDropsPreviousMember(t *testing.T) {
	b, _ := New(10, 10, 1, 3, 5)
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(b.Add("a", 4, 1))
	must(b.Add("b", 3, 1))
	must(b.Add("c", 2, 1))
	r, _ := b.Snapshot(5)
	if len(r.Board) != 3 {
		t.Fatalf("first board %+v", r.Board)
	}
	// New id "a0" ties c at 2 but sorts first in byte order, so c is cut.
	must(b.Add("a0", 2, 6))
	r, _ = b.Snapshot(7)
	if len(r.Board) != 3 || r.Board[2].ID != "a0" {
		t.Fatalf("second board = %+v", r.Board)
	}
	if len(r.Dropped) != 1 || r.Dropped[0] != (DroppedItem{ID: "c", PreviousRank: 3}) {
		t.Fatalf("dropped = %+v", r.Dropped)
	}
}

// Changes positive/negative/zero and a new entry in one snapshot.
func TestRankChanges(t *testing.T) {
	b, _ := New(10, 10, 1, 10, 5)
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(b.Add("a", 10, 1))
	must(b.Add("b", 8, 1))
	must(b.Add("c", 5, 1))
	_, _ = b.Snapshot(5)

	must(b.Add("c", 10, 6)) // c jumps to 15
	must(b.Add("d", 6, 6))
	r, _ := b.Snapshot(7)
	byID := map[string]RankItem{}
	for _, it := range r.Board {
		byID[it.ID] = it
	}
	if byID["c"].Change != 2 { // rank 3 -> 1
		t.Fatalf("c change %d", byID["c"].Change)
	}
	if byID["a"].Change != -1 { // rank 1 -> 2
		t.Fatalf("a change %d", byID["a"].Change)
	}
	if byID["b"].Change != -1 { // rank 2 -> 3
		t.Fatalf("b change %d", byID["b"].Change)
	}
	if !byID["d"].New {
		t.Fatal("d should be new")
	}
}

// Multiple adds between snapshots accumulate.
func TestMultipleAddsBetweenSnapshots(t *testing.T) {
	b, _ := New(10, 10, 50, 10, 1)
	for i := 0; i < 5; i++ {
		if err := b.Add("a", 10, int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	r, err := b.Snapshot(10)
	if err != nil || r.Board[0].Score != 50 {
		t.Fatalf("r=%+v err=%v", r.Board, err)
	}
}

// Repeated snapshots at the same instant report all-zero changes.
func TestRepeatedSnapshotZeroChanges(t *testing.T) {
	b, _ := New(10, 10, 1, 10, 1)
	if err := b.Add("a", 5, 1); err != nil {
		t.Fatal(err)
	}
	r1, _ := b.Snapshot(10)
	r2, err := b.Snapshot(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(r2.Board) != len(r1.Board) || r2.Board[0].Change != 0 || r2.Board[0].New {
		t.Fatalf("repeated snapshot %+v", r2.Board)
	}
	if len(r2.Dropped) != 0 {
		t.Fatalf("unexpected dropped %+v", r2.Dropped)
	}
}

// hc == Hs-1 still uses the low line; hc == Hs forces the M line.
func TestHysteresisThreshold(t *testing.T) {
	b, _ := New(10, 10, 100, 10, 2)
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(b.Add("a", 100, 1))
	r, _ := b.Snapshot(5)
	if r.Board[0].Score != 100 {
		t.Fatal("setup")
	}
	// Move past bucket 0; a keeps 80 from bucket 1 (>= low line 75).
	must(b.Add("a", 80, 10))
	r, _ = b.Snapshot(105) // hc 0 -> 1
	if len(r.Board) != 1 || b.hc["a"] != 1 {
		t.Fatalf("first low-line stay: board=%+v hc=%d", r.Board, b.hc["a"])
	}
	r, _ = b.Snapshot(106) // hc=1 == Hs-1: stays at 75, hc->2
	if len(r.Board) != 1 || b.hc["a"] != 2 {
		t.Fatalf("hc==Hs-1 should stay: board=%+v hc=%d", r.Board, b.hc["a"])
	}
	r, _ = b.Snapshot(107) // hc==Hs: M line restored, drops
	if len(r.Board) != 0 || len(r.Dropped) != 1 {
		t.Fatalf("hc==Hs must drop: board=%+v dropped=%+v", r.Board, r.Dropped)
	}
}

// After dropping out the low line no longer applies; M is required again.
func TestReentryRequiresM(t *testing.T) {
	b, _ := New(10, 10, 100, 10, 1)
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(b.Add("a", 100, 1))
	_, _ = b.Snapshot(5)
	must(b.Add("a", 75, 10))
	_, _ = b.Snapshot(105) // 75 < 100, hc 0->1 via low line
	if b.hc["a"] != 1 {
		t.Fatalf("hc=%d", b.hc["a"])
	}
	r, _ := b.Snapshot(106) // hc==Hs => dropped
	if len(r.Board) != 0 {
		t.Fatalf("a should drop, got %+v", r.Board)
	}
	r, _ = b.Snapshot(107)
	if len(r.Board) != 0 {
		t.Fatalf("a must reach M to re-enter, got %+v", r.Board)
	}
}

// Recovering to >= M resets hc to 0.
func TestRecoveryResetsHc(t *testing.T) {
	b, _ := New(10, 5, 100, 10, 3)
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	// Bucket 0: 100; bucket 1: 80. At cur=4 window is ( -1,4], so the
	// score is 180; at cur=6 window is (1,6], bucket 0 exits -> 80.
	must(b.Add("a", 100, 9))
	_, _ = b.Snapshot(10)
	must(b.Add("a", 80, 12))
	_, _ = b.Snapshot(50) // cur=5, window (0,5]: bucket 0 gone, bucket 1 kept
	if b.hc["a"] != 1 {
		t.Fatalf("hc=%d", b.hc["a"])
	}
	// Add 30 into bucket 2 while it is still in the window: score 110.
	must(b.Add("a", 30, 25))
	r, _ := b.Snapshot(55)
	if r.Board[0].Score != 110 || b.hc["a"] != 0 {
		t.Fatalf("score=%d hc=%d", r.Board[0].Score, b.hc["a"])
	}
}

// Peek never advances the clock or increments hc.
func TestPeekDoesNotAdvanceHc(t *testing.T) {
	b, _ := New(10, 10, 100, 10, 5)
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	must(b.Add("a", 100, 9)) // bucket 0, evicted by Snapshot(105)
	_, _ = b.Snapshot(10)
	must(b.Add("a", 80, 12)) // bucket 1 survives at Peek(105), buckets 1..10
	r, err := b.Peek(105)
	must(err)
	if len(r.Board) != 1 || b.hc["a"] != 0 {
		t.Fatalf("peek changed hc: %+v hc=%d", r.Board, b.hc["a"])
	}
	if b.c != 12 { // the accepted Add itself advances c to 12
		t.Fatalf("clock after add = %d", b.c)
	}
	r2, _ := b.Snapshot(105)
	if len(r2.Board) != 1 || b.hc["a"] != 1 {
		t.Fatalf("snapshot hc: %+v hc=%d", r2.Board, b.hc["a"])
	}
}

// Rejection priority: invalid > expired > rewind > overflow; no state change.
func TestRejectionPriority(t *testing.T) {
	b, _ := New(10, 2, 100, 10, 1)
	if err := b.Add("a", 100, 5); err != nil {
		t.Fatal(err)
	}

	if err := b.Add("", 5, 100); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty id: %v", err)
	}
	if err := b.Add("z", 0, 5); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("d=0: %v", err)
	}
	if err := b.Add("z", 5, -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("t<0: %v", err)
	}
	if err := b.Add("z", 5, maxTime+1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("t>max: %v", err)
	}
	if _, err := b.Snapshot(-1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("now<0: %v", err)
	}
	if _, err := b.Snapshot(maxTime + 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("now>max: %v", err)
	}

	// Overflow: accumulate to the cap (single d <= 1e9), then exceed it.
	for i := 0; i < 1_000_000; i++ {
		if err := b.Add("z", 1_000_000_000, 5); err != nil {
			t.Fatalf("fill z[%d]: %v", i, err)
		}
	}
	if err := b.Add("z", 1, 5); !errors.Is(err, ErrScoreOverflow) {
		t.Fatalf("overflow: %v", err)
	}
	if got := b.Score("z"); got != maxScore {
		t.Fatalf("z score after overflow = %d", got)
	}

	// Advance the clock, then send a late event that is also huge:
	// expiration must be reported before any overflow consideration.
	if _, err := b.Snapshot(35); err != nil {
		t.Fatal(err)
	}
	if err := b.Add("a", 1_000_000_000, 10); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired priority: %v", err)
	}

	// Clock rewind on snapshot and peek.
	if _, err := b.Snapshot(4); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("rewind: %v", err)
	}
	if _, err := b.Peek(4); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("peek rewind: %v", err)
	}

	r, err := b.Snapshot(35)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Board) != 0 { // both bucket-0 entries left the window
		t.Fatalf("board after rejections = %+v", r.Board)
	}
}

// Constructor parameter bounds.
func TestNewBounds(t *testing.T) {
	bad := [][5]int64{
		{0, 3, 100, 3, 1},
		{1_000_000_001, 3, 100, 3, 1},
		{10, 0, 100, 3, 1},
		{10, 1001, 100, 3, 1},
		{10, 3, 0, 3, 1},
		{10, 3, 1_000_000_000_001, 3, 1},
		{10, 3, 100, 0, 1},
		{10, 3, 100, 101, 1},
		{10, 3, 100, 3, 0},
		{10, 3, 100, 3, 101},
	}
	for _, p := range bad {
		if _, err := New(p[0], p[1], p[2], p[3], p[4]); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("New(%v) = %v", p, err)
		}
	}
	if _, err := New(1, 1, 1, 1, 1); err != nil {
		t.Fatalf("minimal valid params: %v", err)
	}
}

// Zero-score ids that leave the board are reclaimed; state does not grow.
func TestZeroScoreReclamation(t *testing.T) {
	b, _ := New(10, 1, 1, 10, 1)
	must := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	for i := 0; i < 200; i++ {
		id := "ephemeral" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		must(b.Add(id, 5, int64(i*10)))
	}
	_, _ = b.Snapshot(1990)
	_, _ = b.Snapshot(2000)
	if len(b.scores) > 2 {
		t.Fatalf("scores map not reclaimed: %d entries", len(b.scores))
	}
}
