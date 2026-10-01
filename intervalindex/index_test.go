package intervalindex

import (
	"errors"
	"testing"
)

func mustInsert(t *testing.T, idx *Index, id, lo, hi int64) {
	t.Helper()
	if err := idx.Insert(id, lo, hi); err != nil {
		t.Fatalf("Insert(%d,[%d,%d)): %v", id, lo, hi, err)
	}
}

func TestStabEndpointSemantics(t *testing.T) {
	idx := New()
	// ids 1 and 2 share (lo, hi); id 5 starts at the shared hi boundary.
	mustInsert(t, idx, 2, 10, 20)
	mustInsert(t, idx, 1, 10, 20)
	mustInsert(t, idx, 5, 15, 30)

	// x == lo is included.
	if got := idx.Stab(10); !equalIDs(got, []int64{1, 2}) {
		t.Fatalf("Stab(10) = %v, want [1 2] (x == lo included)", got)
	}
	if got := idx.Stab(15); !equalIDs(got, []int64{1, 2, 5}) {
		t.Fatalf("Stab(15) = %v, want [1 2 5]", got)
	}
	// x == hi is excluded from the intervals ending there.
	if got := idx.Stab(20); !equalIDs(got, []int64{5}) {
		t.Fatalf("Stab(20) = %v, want [5] (x == hi excluded)", got)
	}
	if got := idx.Stab(30); !equalIDs(got, nil) {
		t.Fatalf("Stab(30) = %v, want [] (x == hi excluded)", got)
	}
}

func TestAdjacentIntervalsDoNotOverlap(t *testing.T) {
	idx := New()
	mustInsert(t, idx, 1, 1, 3)
	mustInsert(t, idx, 2, 3, 5)

	if got, err := idx.Overlap(1, 3); err != nil || !equalIDs(got, []int64{1}) {
		t.Fatalf("Overlap([1,3)) = %v, %v; want [1], nil", got, err)
	}
	if got, err := idx.Overlap(3, 5); err != nil || !equalIDs(got, []int64{2}) {
		t.Fatalf("Overlap([3,5)) = %v, %v; want [2], nil", got, err)
	}
	if got, _ := idx.Overlap(2, 3); !equalIDs(got, []int64{1}) {
		t.Fatalf("Overlap([2,3)) = %v, want [1]", got)
	}
	// The boundary point is hi of the first and lo of the second: only the
	// second contains it.
	if got := idx.Stab(3); !equalIDs(got, []int64{2}) {
		t.Fatalf("Stab(3) = %v, want [2]", got)
	}
	// Direct interval-to-interval half-open check via a point query spanning
	// the boundary: no query interval can cover both across the touch point.
	if got, _ := idx.Overlap(1, 5); !equalIDs(got, []int64{1, 2}) {
		t.Fatalf("Overlap([1,5)) = %v, want [1 2] (both intersect query, not each other)", got)
	}
}

func TestSameEndpointOrdering(t *testing.T) {
	idx := New()
	// Insert ids out of order; results must sort by (lo, id) regardless.
	for _, id := range []int64{30, 3, 10, 200, 1} {
		mustInsert(t, idx, id, 7, 70)
	}
	got := idx.Stab(50)
	want := []int64{1, 3, 10, 30, 200}
	if !equalIDs(got, want) {
		t.Fatalf("same (lo,hi) ids = %v, want %v", got, want)
	}
	got, err := idx.Overlap(0, 100)
	if err != nil || !equalIDs(got, want) {
		t.Fatalf("overlap same (lo,hi) = %v, %v; want %v", got, err, want)
	}
}

func TestContainment(t *testing.T) {
	idx := New()
	mustInsert(t, idx, 1, 0, 100)    // contains the others
	mustInsert(t, idx, 2, 10, 90)    // contained by 1, contains 3
	mustInsert(t, idx, 3, 40, 50)    // innermost
	mustInsert(t, idx, 4, -100, -50) // far away

	got, err := idx.Overlap(40, 50)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{1, 2, 3}; !equalIDs(got, want) {
		t.Fatalf("Overlap([40,50)) = %v, want %v", got, want)
	}
	if got := idx.Stab(45); !equalIDs(got, []int64{1, 2, 3}) {
		t.Fatalf("Stab(45) = %v, want [1 2 3]", got)
	}
	// A query fully contained inside the innermost interval hits all three.
	if got, _ := idx.Overlap(44, 46); !equalIDs(got, []int64{1, 2, 3}) {
		t.Fatalf("narrow Overlap = %v, want [1 2 3]", got)
	}
}

func TestRejectionCausesAndAtomicity(t *testing.T) {
	idx := New()
	// Insert validation order: non-positive id, empty interval, duplicate.
	if err := idx.Insert(0, 1, 2); !errors.Is(err, ErrNonPositiveID) {
		t.Fatalf("Insert id=0: %v, want ErrNonPositiveID", err)
	}
	if err := idx.Insert(-3, 5, 4); !errors.Is(err, ErrNonPositiveID) {
		t.Fatalf("Insert id=-3 with lo>=hi: %v, want first cause ErrNonPositiveID", err)
	}
	if err := idx.Insert(1, 5, 5); !errors.Is(err, ErrEmptyInterval) {
		t.Fatalf("Insert lo==hi: %v, want ErrEmptyInterval", err)
	}
	if err := idx.Insert(1, 9, 3); !errors.Is(err, ErrEmptyInterval) {
		t.Fatalf("Insert lo>hi: %v, want ErrEmptyInterval", err)
	}
	if err := idx.Insert(1, 1, 2); err != nil {
		t.Fatal(err)
	}
	if err := idx.Insert(1, 3, 4); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("duplicate id: %v, want ErrDuplicateID", err)
	}
	if idx.Len() != 1 {
		t.Fatalf("Len = %d after rejections, want 1", idx.Len())
	}

	// Remove unknown id is rejected.
	if err := idx.Remove(99); !errors.Is(err, ErrIDNotFound) {
		t.Fatalf("Remove missing: %v, want ErrIDNotFound", err)
	}

	// Zero-width and inverted query ranges are rejected.
	if got, err := idx.Overlap(4, 4); !errors.Is(err, ErrInvalidQuery) || got != nil {
		t.Fatalf("Overlap a==b: got=%v err=%v, want nil ErrInvalidQuery", got, err)
	}
	if _, err := idx.Overlap(5, 2); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("Overlap a>b: err=%v, want ErrInvalidQuery", err)
	}
}

func TestRemoveThenReinsertSameID(t *testing.T) {
	idx := New()
	mustInsert(t, idx, 7, 1, 10)
	mustInsert(t, idx, 8, 2, 11)
	if err := idx.Remove(7); err != nil {
		t.Fatal(err)
	}
	if got := idx.Stab(5); !equalIDs(got, []int64{8}) {
		t.Fatalf("after remove, Stab(5) = %v, want [8]", got)
	}
	// Same id comes back with a different geometry and must be re-indexed.
	mustInsert(t, idx, 7, 100, 200)
	if got := idx.Stab(5); !equalIDs(got, []int64{8}) {
		t.Fatalf("after reinsert, Stab(5) = %v, want [8]", got)
	}
	if got := idx.Stab(150); !equalIDs(got, []int64{7}) {
		t.Fatalf("after reinsert, Stab(150) = %v, want [7]", got)
	}
	if idx.Len() != 2 {
		t.Fatalf("Len = %d, want 2", idx.Len())
	}
	if err := idx.Remove(7); err != nil {
		t.Fatal(err)
	}
	if err := idx.Remove(7); !errors.Is(err, ErrIDNotFound) {
		t.Fatalf("second remove: %v, want ErrIDNotFound", err)
	}
}
