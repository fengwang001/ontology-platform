package tracker

import (
	"errors"
	"testing"
)

func mustAddPoint(t *testing.T, tr *Tracker, pos int, b Bias) int {
	t.Helper()
	id, err := tr.AddPoint(pos, b)
	if err != nil {
		t.Fatalf("AddPoint(%d, %v): %v", pos, b, err)
	}
	return id
}

func mustAddRange(t *testing.T, tr *Tracker, s, e int, kind RangeKind) int {
	t.Helper()
	id, err := tr.AddRange(s, e, kind)
	if err != nil {
		t.Fatalf("AddRange(%d, %d, %v): %v", s, e, kind, err)
	}
	return id
}

func mustPos(t *testing.T, tr *Tracker, id, asRev, want int) {
	t.Helper()
	got, err := tr.Pos(id, asRev)
	if err != nil {
		t.Fatalf("Pos(%d, %d): %v", id, asRev, err)
	}
	if got != want {
		t.Errorf("Pos(%d, %d) = %d, want %d", id, asRev, got, want)
	}
}

func mustRange(t *testing.T, tr *Tracker, id, asRev, ws, we int, wcol bool) {
	t.Helper()
	s, e, col, err := tr.Range(id, asRev)
	if err != nil {
		t.Fatalf("Range(%d, %d): %v", id, asRev, err)
	}
	if s != ws || e != we || col != wcol {
		t.Errorf("Range(%d, %d) = (%d, %d, %v), want (%d, %d, %v)",
			id, asRev, s, e, col, ws, we, wcol)
	}
}

func mustReplace(t *testing.T, tr *Tracker, p, d, n int) {
	t.Helper()
	if _, err := tr.Replace(p, d, n); err != nil {
		t.Fatalf("Replace(%d, %d, %d): %v", p, d, n, err)
	}
}

func mustMove(t *testing.T, tr *Tracker, p, length, q int) {
	t.Helper()
	if _, err := tr.Move(p, length, q); err != nil {
		t.Fatalf("Move(%d, %d, %d): %v", p, length, q, err)
	}
}

// pointMap applies a single edit on a fresh doc of length n0 and checks
// where an anchor created at (x, b) on rev 0 lands on rev 1.
func pointMap(t *testing.T, n0, x int, b Bias, edit func(tr *Tracker), want int) {
	t.Helper()
	tr := NewTracker(n0, 1<<20, 64)
	id := mustAddPoint(t, tr, x, b)
	edit(tr)
	mustPos(t, tr, id, tr.Rev(), want)
}

func repl(t *testing.T, p, d, n int) func(tr *Tracker) {
	return func(tr *Tracker) { mustReplace(t, tr, p, d, n) }
}

func mov(t *testing.T, p, length, q int) func(tr *Tracker) {
	return func(tr *Tracker) { mustMove(t, tr, p, length, q) }
}

func TestSpecReplaceExample(t *testing.T) {
	// L=10, Replace(3,2,4): x=5 Left->3 Right->7, x=3 Left->3 Right->7, x=6->8.
	pointMap(t, 10, 5, Left, repl(t, 3, 2, 4), 3)
	pointMap(t, 10, 5, Right, repl(t, 3, 2, 4), 7)
	pointMap(t, 10, 3, Left, repl(t, 3, 2, 4), 3)
	pointMap(t, 10, 3, Right, repl(t, 3, 2, 4), 7)
	pointMap(t, 10, 6, Left, repl(t, 3, 2, 4), 8)
	pointMap(t, 10, 6, Right, repl(t, 3, 2, 4), 8)
}

func TestSpecMoveExample(t *testing.T) {
	// L=10, Move(2,3,8) (q'=5): x=2 L->2 R->5, x=5 L->8 R->2,
	// x=8 L->5 R->8, x=9->9.
	pointMap(t, 10, 2, Left, mov(t, 2, 3, 8), 2)
	pointMap(t, 10, 2, Right, mov(t, 2, 3, 8), 5)
	pointMap(t, 10, 5, Left, mov(t, 2, 3, 8), 8)
	pointMap(t, 10, 5, Right, mov(t, 2, 3, 8), 2)
	pointMap(t, 10, 8, Left, mov(t, 2, 3, 8), 5)
	pointMap(t, 10, 8, Right, mov(t, 2, 3, 8), 8)
	pointMap(t, 10, 9, Left, mov(t, 2, 3, 8), 9)
	pointMap(t, 10, 9, Right, mov(t, 2, 3, 8), 9)
}

func TestSpecRangeExamples(t *testing.T) {
	// Replace(3,2,4) on L=10: [3,5) Tight -> [3,3) collapsed, Loose -> [3,7).
	tr := NewTracker(10, 1<<20, 64)
	tight := mustAddRange(t, tr, 3, 5, Tight)
	loose := mustAddRange(t, tr, 3, 5, Loose)
	mustReplace(t, tr, 3, 2, 4)
	mustRange(t, tr, tight, 1, 3, 3, true)
	mustRange(t, tr, loose, 1, 3, 7, false)

	// Move(3,1,10) on L=10: [3,6) Tight -> [5,5) collapsed.
	tr2 := NewTracker(10, 1<<20, 64)
	r := mustAddRange(t, tr2, 3, 6, Tight)
	mustMove(t, tr2, 3, 1, 10)
	mustRange(t, tr2, r, 1, 5, 5, true)
}

func TestReplaceBoundaries(t *testing.T) {
	// d=0 (pure insert) at p=4, n=3: x=p stays before (Left) / jumps
	// after (Right) the inserted text.
	pointMap(t, 10, 4, Left, repl(t, 4, 0, 3), 4)
	pointMap(t, 10, 4, Right, repl(t, 4, 0, 3), 7)
	// d>0 at p=4, d=2, n=1: x=p and x=p+d both land on the edited span.
	pointMap(t, 10, 4, Left, repl(t, 4, 2, 1), 4)
	pointMap(t, 10, 4, Right, repl(t, 4, 2, 1), 5)
	pointMap(t, 10, 6, Left, repl(t, 4, 2, 1), 4)
	pointMap(t, 10, 6, Right, repl(t, 4, 2, 1), 5)
	// d>0 pure delete (n=0): both boundaries collapse to p.
	pointMap(t, 10, 4, Left, repl(t, 4, 2, 0), 4)
	pointMap(t, 10, 4, Right, repl(t, 4, 2, 0), 4)
	pointMap(t, 10, 6, Left, repl(t, 4, 2, 0), 4)
	pointMap(t, 10, 6, Right, repl(t, 4, 2, 0), 4)
	// Outside the span: x=p-1 unchanged, x=p+d+1 shifted by n-d.
	pointMap(t, 10, 3, Left, repl(t, 4, 2, 1), 3)
	pointMap(t, 10, 3, Right, repl(t, 4, 2, 1), 3)
	pointMap(t, 10, 7, Left, repl(t, 4, 2, 1), 6)
	pointMap(t, 10, 7, Right, repl(t, 4, 2, 1), 6)
}

// TestMoveBoundariesForward covers every gap of L=10 under Move(2,3,8)
// (q'=5) for both biases.
func TestMoveBoundariesForward(t *testing.T) {
	want := [11][2]int{
		0:  {0, 0},
		1:  {1, 1},
		2:  {2, 5}, // x==p: Left stays, Right rides the segment
		3:  {6, 6}, // interior of the moved segment
		4:  {7, 7},
		5:  {8, 2}, // x==p+len: Left rides, Right stays behind
		6:  {3, 3},
		7:  {4, 4},
		8:  {5, 8}, // y==q': Left before, Right after the insertion
		9:  {9, 9},
		10: {10, 10},
	}
	for x := 0; x <= 10; x++ {
		pointMap(t, 10, x, Left, mov(t, 2, 3, 8), want[x][0])
		pointMap(t, 10, x, Right, mov(t, 2, 3, 8), want[x][1])
	}
}

// TestMoveBoundariesBackward covers every gap of L=10 under Move(5,3,1)
// (q'=1) for both biases.
func TestMoveBoundariesBackward(t *testing.T) {
	want := [11][2]int{
		0:  {0, 0},
		1:  {1, 4}, // y==q': Left before, Right after the insertion
		2:  {5, 5},
		3:  {6, 6},
		4:  {7, 7},
		5:  {8, 1}, // x==p: Left stays, Right rides the segment
		6:  {2, 2}, // interior of the moved segment
		7:  {3, 3},
		8:  {4, 8}, // x==p+len: Left rides, Right stays behind
		9:  {9, 9},
		10: {10, 10},
	}
	for x := 0; x <= 10; x++ {
		pointMap(t, 10, x, Left, mov(t, 5, 3, 1), want[x][0])
		pointMap(t, 10, x, Right, mov(t, 5, 3, 1), want[x][1])
	}
}

func TestMoveRejected(t *testing.T) {
	cases := [][4]int{
		{2, 3, 2},   // q == p
		{2, 3, 5},   // q == p+len
		{2, 3, 3},   // q inside (p, p+len)
		{2, 3, 4},   // q inside (p, p+len)
		{2, 0, 7},   // len < 1
		{-1, 2, 5},  // p < 0
		{9, 2, 0},   // p+len > L
		{2, 3, -1},  // q < 0
		{2, 3, 11},  // q > L
		{2, 3, 100}, // q > L
	}
	for _, c := range cases {
		tr := NewTracker(10, 1<<20, 64)
		id := mustAddPoint(t, tr, 4, Left)
		if _, err := tr.Move(c[0], c[1], c[2]); !errors.Is(err, ErrInvalid) {
			t.Errorf("Move%v: got %v, want ErrInvalid", c, err)
		}
		if tr.Rev() != 0 || tr.Len() != 10 {
			t.Errorf("Move%v mutated state: rev=%d len=%d", c, tr.Rev(), tr.Len())
		}
		mustPos(t, tr, id, 0, 4)
	}
}

func TestReplaceRejected(t *testing.T) {
	tr := NewTracker(10, 12, 64)
	id := mustAddPoint(t, tr, 4, Right)
	// Invalid: negative args, d+n==0, p+d>L.
	for _, c := range [][3]int{{-1, 1, 1}, {1, -1, 1}, {1, 1, -1}, {1, 0, 0}, {9, 2, 0}} {
		if _, err := tr.Replace(c[0], c[1], c[2]); !errors.Is(err, ErrInvalid) {
			t.Errorf("Replace%v: got %v, want ErrInvalid", c, err)
		}
	}
	// TooLarge: valid edit exceeding MaxLen=12.
	if _, err := tr.Replace(0, 0, 3); !errors.Is(err, ErrTooLarge) {
		t.Errorf("Replace(0,0,3): got %v, want ErrTooLarge", err)
	}
	// ErrInvalid wins over ErrTooLarge.
	if _, err := tr.Replace(9, 2, 100); !errors.Is(err, ErrInvalid) {
		t.Errorf("Replace(9,2,100): got %v, want ErrInvalid", err)
	}
	if tr.Rev() != 0 || tr.Len() != 10 {
		t.Fatalf("rejected edits mutated state: rev=%d len=%d", tr.Rev(), tr.Len())
	}
	mustPos(t, tr, id, 0, 4)
	// A valid grow to exactly MaxLen succeeds.
	mustReplace(t, tr, 0, 0, 2)
	if tr.Len() != 12 {
		t.Fatalf("Len = %d, want 12", tr.Len())
	}
}

func TestCollapsedThenInsert(t *testing.T) {
	// Tight [3,5) collapses to [3,3) under Replace(3,2,4); a later insert
	// at 3 keeps it collapsed (Right start jumps, Left end stays).
	tr := NewTracker(10, 1<<20, 64)
	tight := mustAddRange(t, tr, 3, 5, Tight)
	mustReplace(t, tr, 3, 2, 4)
	mustRange(t, tr, tight, 1, 3, 3, true)
	mustReplace(t, tr, 3, 0, 2)
	mustRange(t, tr, tight, 2, 3, 3, true)

	// A collapsed Loose [3,3) re-expands to [3,5) on the same insert.
	tr2 := NewTracker(10, 1<<20, 64)
	mustReplace(t, tr2, 3, 2, 4)
	loose := mustAddRange(t, tr2, 3, 3, Loose)
	mustRange(t, tr2, loose, 1, 3, 3, true)
	mustReplace(t, tr2, 3, 0, 2)
	mustRange(t, tr2, loose, 2, 3, 5, false)
}

func TestCompactBoundary(t *testing.T) {
	tr := NewTracker(10, 1<<20, 64)
	p := mustAddPoint(t, tr, 5, Right)    // createdRev 0
	r := mustAddRange(t, tr, 2, 8, Loose) // createdRev 0
	mustReplace(t, tr, 0, 0, 2)           // rev 1
	mustReplace(t, tr, 4, 1, 3)           // rev 2
	late := mustAddPoint(t, tr, 1, Left)  // createdRev 2
	mustMove(t, tr, 1, 2, 9)              // rev 3

	before := map[int]int{}
	for _, rev := range []int{0, 1, 2, 3} {
		pos, err := tr.Pos(p, rev)
		if err != nil {
			t.Fatalf("Pos(%d, %d): %v", p, rev, err)
		}
		before[rev] = pos
	}

	if err := tr.Compact(2); err != nil {
		t.Fatalf("Compact(2): %v", err)
	}
	if tr.Floor() != 2 {
		t.Fatalf("Floor = %d, want 2", tr.Floor())
	}

	// asRev == checkpoint (== newFloor) still works; same answers as before.
	for _, rev := range []int{2, 3} {
		pos, err := tr.Pos(p, rev)
		if err != nil {
			t.Fatalf("Pos(%d, %d) after compact: %v", p, rev, err)
		}
		if pos != before[rev] {
			t.Errorf("Pos(%d, %d) = %d after compact, was %d", p, rev, pos, before[rev])
		}
	}
	// asRev < checkpoint -> ErrCompacted (boundary at newFloor-1).
	for _, rev := range []int{0, 1} {
		if _, err := tr.Pos(p, rev); !errors.Is(err, ErrCompacted) {
			t.Errorf("Pos(%d, %d) after compact: got %v, want ErrCompacted", p, rev, err)
		}
		if _, _, _, err := tr.Range(r, rev); !errors.Is(err, ErrCompacted) {
			t.Errorf("Range(%d, %d) after compact: got %v, want ErrCompacted", r, rev, err)
		}
	}
	// Anchor created at rev 2 keeps its checkpoint: rev 2 and 3 queryable.
	if _, err := tr.Pos(late, 2); err != nil {
		t.Errorf("Pos(late, 2): %v", err)
	}
	if _, err := tr.Pos(late, 3); err != nil {
		t.Errorf("Pos(late, 3): %v", err)
	}
	// Range answers identical across compaction.
	s1, e1, c1, err := tr.Range(r, 3)
	if err != nil {
		t.Fatalf("Range(%d, 3): %v", r, err)
	}
	tr2 := NewTracker(10, 1<<20, 64)
	r2 := mustAddRange(t, tr2, 2, 8, Loose)
	mustReplace(t, tr2, 0, 0, 2)
	mustReplace(t, tr2, 4, 1, 3)
	mustMove(t, tr2, 1, 2, 9)
	s2, e2, c2, err := tr2.Range(r2, 3)
	if err != nil {
		t.Fatalf("uncompacted Range: %v", err)
	}
	if s1 != s2 || e1 != e2 || c1 != c2 {
		t.Errorf("compacted (%d,%d,%v) != uncompacted (%d,%d,%v)", s1, e1, c1, s2, e2, c2)
	}
}

func TestCompactBadFloor(t *testing.T) {
	tr := NewTracker(10, 1<<20, 64)
	mustReplace(t, tr, 0, 0, 1)
	if err := tr.Compact(2); !errors.Is(err, ErrBadFloor) {
		t.Errorf("Compact(2) with rev=1: got %v, want ErrBadFloor", err)
	}
	if err := tr.Compact(-1); !errors.Is(err, ErrBadFloor) {
		t.Errorf("Compact(-1): got %v, want ErrBadFloor", err)
	}
	if err := tr.Compact(1); err != nil {
		t.Fatalf("Compact(1): %v", err)
	}
	if err := tr.Compact(0); !errors.Is(err, ErrBadFloor) {
		t.Errorf("Compact(0) below floor=1: got %v, want ErrBadFloor", err)
	}
	if err := tr.Compact(1); err != nil {
		t.Errorf("Compact(1) at floor: %v", err)
	}
}

func TestReplayedCounter(t *testing.T) {
	tr := NewTracker(10, 1<<20, 64)
	p := mustAddPoint(t, tr, 5, Right)
	r := mustAddRange(t, tr, 2, 8, Loose)
	mustReplace(t, tr, 0, 0, 2) // rev 1
	mustReplace(t, tr, 4, 1, 3) // rev 2
	mustMove(t, tr, 1, 2, 9)    // rev 3

	if tr.replayed != 0 {
		t.Fatalf("replayed = %d before any query, want 0", tr.replayed)
	}
	if _, err := tr.Pos(p, 3); err != nil {
		t.Fatal(err)
	}
	if tr.replayed != 3 {
		t.Fatalf("replayed = %d, want 3 (asRev 3 - checkpoint 0)", tr.replayed)
	}
	if _, err := tr.Pos(p, 1); err != nil {
		t.Fatal(err)
	}
	if tr.replayed != 4 {
		t.Fatalf("replayed = %d, want 4 (+1)", tr.replayed)
	}
	// Compact(2) materializes both anchors: +2 each.
	if err := tr.Compact(2); err != nil {
		t.Fatal(err)
	}
	if tr.replayed != 8 {
		t.Fatalf("replayed = %d, want 8 (+2 per materialized anchor)", tr.replayed)
	}
	// Query from the new checkpoint replays exactly asRev-checkpoint edits.
	if _, _, _, err := tr.Range(r, 3); err != nil {
		t.Fatal(err)
	}
	if tr.replayed != 9 {
		t.Fatalf("replayed = %d, want 9 (+1)", tr.replayed)
	}
	// Zero-cost query at the checkpoint itself.
	if _, err := tr.Pos(p, 2); err != nil {
		t.Fatal(err)
	}
	if tr.replayed != 9 {
		t.Fatalf("replayed = %d, want 9 (+0)", tr.replayed)
	}
}

func TestQueryErrorPriority(t *testing.T) {
	tr := NewTracker(10, 1<<20, 64)
	p := mustAddPoint(t, tr, 5, Right)
	r := mustAddRange(t, tr, 2, 8, Tight)
	mustReplace(t, tr, 0, 0, 1) // rev 1

	// ErrNoAnchor beats ErrWrongKind and ErrFuture.
	if err := tr.Remove(r); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Pos(r, 99); !errors.Is(err, ErrNoAnchor) {
		t.Errorf("Pos(removed range, 99): got %v, want ErrNoAnchor", err)
	}
	if _, _, _, err := tr.Range(12345, 0); !errors.Is(err, ErrNoAnchor) {
		t.Errorf("Range(unknown): got %v, want ErrNoAnchor", err)
	}
	// ErrWrongKind beats ErrFuture/ErrNotYet/ErrCompacted.
	r2 := mustAddRange(t, tr, 0, 1, Loose)
	if _, err := tr.Pos(r2, 99); !errors.Is(err, ErrWrongKind) {
		t.Errorf("Pos(range, 99): got %v, want ErrWrongKind", err)
	}
	if _, _, _, err := tr.Range(p, 99); !errors.Is(err, ErrWrongKind) {
		t.Errorf("Range(point, 99): got %v, want ErrWrongKind", err)
	}
	// ErrFuture beats ErrNotYet.
	if _, err := tr.Pos(p, 99); !errors.Is(err, ErrFuture) {
		t.Errorf("Pos(point, 99): got %v, want ErrFuture", err)
	}
	// ErrNotYet beats ErrCompacted: create at rev 2, compact to 2, ask rev 1.
	mustReplace(t, tr, 0, 0, 1) // rev 2
	late := mustAddPoint(t, tr, 0, Left)
	if err := tr.Compact(2); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Pos(late, 1); !errors.Is(err, ErrNotYet) {
		t.Errorf("Pos(late, 1): got %v, want ErrNotYet", err)
	}
	// ErrCompacted when createdRev <= asRev < checkpointRev.
	if _, err := tr.Pos(p, 1); !errors.Is(err, ErrCompacted) {
		t.Errorf("Pos(p, 1) with checkpoint 2: got %v, want ErrCompacted", err)
	}
}

func TestAddAndRemove(t *testing.T) {
	tr := NewTracker(10, 1<<20, 2)
	if _, err := tr.AddPoint(-1, Left); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("AddPoint(-1): got %v, want ErrOutOfRange", err)
	}
	if _, err := tr.AddPoint(11, Left); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("AddPoint(11): got %v, want ErrOutOfRange", err)
	}
	if _, err := tr.AddRange(5, 3, Tight); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("AddRange(5,3): got %v, want ErrOutOfRange", err)
	}
	if _, err := tr.AddRange(0, 11, Loose); !errors.Is(err, ErrOutOfRange) {
		t.Errorf("AddRange(0,11): got %v, want ErrOutOfRange", err)
	}
	a := mustAddPoint(t, tr, 0, Left)
	b := mustAddRange(t, tr, 0, 10, Loose)
	if _, err := tr.AddPoint(1, Right); !errors.Is(err, ErrTooMany) {
		t.Errorf("AddPoint beyond cap: got %v, want ErrTooMany", err)
	}
	// Remove frees capacity; ids are never reused.
	if err := tr.Remove(a); err != nil {
		t.Fatal(err)
	}
	if err := tr.Remove(a); !errors.Is(err, ErrNoAnchor) {
		t.Errorf("double Remove: got %v, want ErrNoAnchor", err)
	}
	c := mustAddPoint(t, tr, 1, Right)
	if c == a || c == b {
		t.Errorf("id %d reused", c)
	}
	if _, err := tr.Pos(a, 0); !errors.Is(err, ErrNoAnchor) {
		t.Errorf("Pos(removed): got %v, want ErrNoAnchor", err)
	}
	mustPos(t, tr, c, 0, 1)
}
