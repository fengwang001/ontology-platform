package anchor

import (
	"errors"
	"sync"
	"testing"
)

func requireError(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

func requirePoint(t *testing.T, tr *Tracker, id, rev, want int) {
	t.Helper()
	got, err := tr.Pos(id, rev)
	if err != nil {
		t.Fatalf("Pos(%d, %d): %v", id, rev, err)
	}
	if got != want {
		t.Fatalf("Pos(%d, %d) = %d, want %d", id, rev, got, want)
	}
}

func TestReplaceSpecExample(t *testing.T) {
	tr := New(10, 20, 10)
	left5, _ := tr.AddPoint(5, Left)
	right5, _ := tr.AddPoint(5, Right)
	left3, _ := tr.AddPoint(3, Left)
	right3, _ := tr.AddPoint(3, Right)
	left6, _ := tr.AddPoint(6, Left)
	right6, _ := tr.AddPoint(6, Right)

	rev, err := tr.Replace(3, 2, 4)
	if err != nil || rev != 1 {
		t.Fatalf("Replace = (%d, %v), want (1, nil)", rev, err)
	}

	requirePoint(t, tr, left5, 1, 3)
	requirePoint(t, tr, right5, 1, 7)
	requirePoint(t, tr, left3, 1, 3)
	requirePoint(t, tr, right3, 1, 7)
	requirePoint(t, tr, left6, 1, 8)
	requirePoint(t, tr, right6, 1, 8)
}

func TestReplaceInsertionAndDeletionBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		deleted int
		p       int
		pos     int
		bias    Bias
		want    int
	}{
		{"insert left", 0, 3, 3, Left, 3},
		{"insert right", 0, 3, 3, Right, 7},
		{"delete start left", 2, 3, 3, Left, 3},
		{"delete start right", 2, 3, 3, Right, 7},
		{"delete end left", 2, 3, 5, Left, 3},
		{"delete end right", 2, 3, 5, Right, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := New(10, 20, 1)
			id, _ := tr.AddPoint(tc.pos, tc.bias)
			if _, err := tr.Replace(tc.p, tc.deleted, 4); err != nil {
				t.Fatalf("Replace: %v", err)
			}
			requirePoint(t, tr, id, 1, tc.want)
		})
	}
}

func TestMoveSpecExample(t *testing.T) {
	tr := New(10, 20, 22)
	want := map[int][2]int{
		0:  {0, 0},
		1:  {1, 1},
		2:  {2, 5},
		3:  {6, 6},
		4:  {7, 7},
		5:  {8, 2},
		6:  {3, 3},
		7:  {4, 4},
		8:  {5, 8},
		9:  {9, 9},
		10: {10, 10},
	}
	ids := map[int][2]int{}
	for pos := 0; pos <= 10; pos++ {
		left, _ := tr.AddPoint(pos, Left)
		right, _ := tr.AddPoint(pos, Right)
		ids[pos] = [2]int{left, right}
	}

	if _, err := tr.Move(2, 3, 8); err != nil {
		t.Fatalf("Move: %v", err)
	}
	for pos := 0; pos <= 10; pos++ {
		requirePoint(t, tr, ids[pos][0], 1, want[pos][0])
		requirePoint(t, tr, ids[pos][1], 1, want[pos][1])
	}
}

func TestMoveBackwardAllBoundaryGaps(t *testing.T) {
	tr := New(10, 20, 22)
	want := map[int][2]int{
		0:  {0, 0},
		1:  {1, 1},
		2:  {2, 5},
		3:  {6, 6},
		4:  {7, 7},
		5:  {8, 8},
		6:  {9, 2},
		7:  {3, 3},
		8:  {4, 4},
		9:  {5, 9},
		10: {10, 10},
	}
	ids := map[int][2]int{}
	for pos := 0; pos <= 10; pos++ {
		left, _ := tr.AddPoint(pos, Left)
		right, _ := tr.AddPoint(pos, Right)
		ids[pos] = [2]int{left, right}
	}

	if _, err := tr.Move(6, 3, 2); err != nil {
		t.Fatalf("Move: %v", err)
	}
	for pos := 0; pos <= 10; pos++ {
		requirePoint(t, tr, ids[pos][0], 1, want[pos][0])
		requirePoint(t, tr, ids[pos][1], 1, want[pos][1])
	}
}

func TestMoveRejections(t *testing.T) {
	for _, q := range []int{2, 4, 5} {
		tr := New(10, 10, 1)
		if _, err := tr.Move(2, 3, q); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Move(..., q=%d) error = %v, want ErrInvalid", q, err)
		}
	}
	tr := New(10, 10, 1)
	_, err := tr.Move(2, 3, 8)
	requireError(t, err, nil)
	if tr.Rev() != 1 {
		t.Fatalf("rev = %d, want 1", tr.Rev())
	}
}

func TestRangeExamples(t *testing.T) {
	tr := New(10, 20, 2)
	tight, _ := tr.AddRange(3, 5, Tight)
	loose, _ := tr.AddRange(3, 5, Loose)
	if _, err := tr.Replace(3, 2, 4); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	s, e, collapsed, err := tr.Range(tight, 1)
	if err != nil || s != 3 || e != 3 || !collapsed {
		t.Fatalf("tight = (%d,%d,%v,%v), want (3,3,true,nil)", s, e, collapsed, err)
	}
	s, e, collapsed, err = tr.Range(loose, 1)
	if err != nil || s != 3 || e != 7 || collapsed {
		t.Fatalf("loose = (%d,%d,%v,%v), want (3,7,false,nil)", s, e, collapsed, err)
	}

	other := New(10, 20, 1)
	id, _ := other.AddRange(3, 6, Tight)
	if _, err := other.Move(3, 1, 10); err != nil {
		t.Fatalf("Move: %v", err)
	}
	s, e, collapsed, err = other.Range(id, 1)
	if err != nil || s != 5 || e != 5 || !collapsed {
		t.Fatalf("moved tight = (%d,%d,%v,%v), want (5,5,true,nil)", s, e, collapsed, err)
	}
}

func TestCollapsedRangeThenInsertion(t *testing.T) {
	tr := New(10, 20, 2)
	tight, _ := tr.AddRange(3, 5, Tight)
	loose, _ := tr.AddRange(3, 5, Loose)
	if _, err := tr.Replace(3, 2, 4); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Replace(3, 0, 2); err != nil {
		t.Fatal(err)
	}

	s, e, collapsed, _ := tr.Range(tight, 2)
	if s != 3 || e != 3 || !collapsed {
		t.Fatalf("tight after insertion = [%d,%d) collapsed=%v", s, e, collapsed)
	}
	s, e, collapsed, _ = tr.Range(loose, 2)
	if s != 3 || e != 9 || collapsed {
		t.Fatalf("loose after insertion = [%d,%d) collapsed=%v", s, e, collapsed)
	}
}

func TestAddRemoveAndRejectionPriority(t *testing.T) {
	tr := New(2, 10, 1)
	id, err := tr.AddPoint(2, Left)
	if err != nil || id != 1 {
		t.Fatalf("AddPoint = (%d,%v)", id, err)
	}
	_, err = tr.AddPoint(0, Left)
	requireError(t, err, ErrTooMany)
	_, err = tr.AddRange(1, 2, Tight)
	requireError(t, err, ErrTooMany)
	_, err = tr.AddPoint(3, Left)
	requireError(t, err, ErrOutOfRange)
	_, err = tr.AddRange(2, 1, Tight)
	requireError(t, err, ErrOutOfRange)

	if _, err := tr.Pos(99, 0); !errors.Is(err, ErrNoAnchor) {
		t.Fatalf("missing Pos error = %v", err)
	}
	if _, _, _, err := tr.Range(id, 0); !errors.Is(err, ErrWrongKind) {
		t.Fatalf("wrong kind error = %v", err)
	}
	_, err = tr.Pos(id, 1)
	requireError(t, err, ErrFuture)

	if _, err := tr.Replace(1, 3, 1); err != ErrInvalid {
		t.Fatalf("invalid/too-large error = %v, want ErrInvalid", err)
	}
	if _, err := tr.Replace(0, 0, 9); err != ErrTooLarge {
		t.Fatalf("too-large error = %v", err)
	}
	if tr.Rev() != 0 || tr.Len() != 2 {
		t.Fatalf("rejected edit changed state: rev=%d len=%d", tr.Rev(), tr.Len())
	}

	if err := tr.Remove(id); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	_, err = tr.Pos(id, 0)
	requireError(t, err, ErrNoAnchor)
	id2, _ := tr.AddPoint(0, Right)
	if id2 != 2 {
		t.Fatalf("reused id = %d", id2)
	}
}

func TestCompactQueriesAndReplayCounts(t *testing.T) {
	tr := New(10, 30, 3)
	oldPoint, _ := tr.AddPoint(6, Right)
	oldRange, _ := tr.AddRange(2, 6, Tight)
	if _, err := tr.Replace(2, 0, 2); err != nil {
		t.Fatal(err)
	}
	midPoint, _ := tr.AddPoint(5, Left)
	if _, err := tr.Replace(7, 1, 0); err != nil {
		t.Fatal(err)
	}

	requirePoint(t, tr, oldPoint, 1, 8)
	if tr.replayed != 1 {
		t.Fatalf("replayed = %d, want 1", tr.replayed)
	}
	s, e, _, err := tr.Range(oldRange, 1)
	if err != nil || s != 4 || e != 8 {
		t.Fatalf("old range at 1 = [%d,%d),%v", s, e, err)
	}
	if err := tr.Compact(1); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if tr.replayed != 2 {
		t.Fatalf("compact replayed = %d, want 2", tr.replayed)
	}

	requirePoint(t, tr, oldPoint, 1, 8)
	if tr.replayed != 0 {
		t.Fatalf("post-compact replay = %d, want 0", tr.replayed)
	}
	s, e, collapsed, err := tr.Range(oldRange, 1)
	if err != nil || s != 4 || e != 8 || collapsed {
		t.Fatalf("compact old range = [%d,%d), %v, %v", s, e, collapsed, err)
	}
	requirePoint(t, tr, midPoint, 1, 5)
	if tr.replayed != 0 {
		t.Fatalf("newer anchor replay = %d, want 0", tr.replayed)
	}
	_, err = tr.Pos(oldPoint, 0)
	requireError(t, err, ErrCompacted)
	requireError(t, tr.Compact(0), ErrBadFloor)
	requireError(t, tr.Compact(3), ErrBadFloor)
}

func TestQueryPriorityNotYetBeforeCompacted(t *testing.T) {
	tr := New(5, 10, 1)
	if _, err := tr.Replace(0, 0, 1); err != nil {
		t.Fatal(err)
	}
	id, _ := tr.AddPoint(2, Left)
	if err := tr.Compact(1); err != nil {
		t.Fatal(err)
	}
	_, err := tr.Pos(id, 0)
	requireError(t, err, ErrNotYet)
}

func TestConcurrentOperations(t *testing.T) {
	tr := New(100, 1000, 100)
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				p := (seed*7 + i) % 50
				rev, err := tr.Replace(p, 0, 1)
				if err == nil {
					if rev < 1 || tr.Len() > 1000 {
						t.Errorf("bad successful edit rev=%d len=%d", rev, tr.Len())
					}
					tr.Rev()
				}
			}
		}(worker)
	}
	wg.Wait()
}
