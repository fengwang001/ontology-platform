package topk

import (
	"math/rand"
	"testing"
)

// A tie straddling the K/K+1 boundary must keep the lexicographically
// smaller ID, not the one that arrived first.
func TestTieAcrossBoundaryKeepsSmallerIDDesc(t *testing.T) {
	s, err := New(2, Desc)
	if err != nil {
		t.Fatal(err)
	}
	s.Push("best", 10.0)
	s.Push("zulu", 5.0) // arrives first at the boundary score
	s.Push("able", 5.0) // same score, smaller ID must evict "zulu"
	assertOrder(t, s, []string{"best", "able"})
}

func TestTieAcrossBoundaryKeepsSmallerIDAsc(t *testing.T) {
	s, err := New(2, Asc)
	if err != nil {
		t.Fatal(err)
	}
	s.Push("best", 1.0)
	s.Push("zulu", 5.0)
	s.Push("able", 5.0)
	assertOrder(t, s, []string{"best", "able"})
}

// Boundary tie decided by ID even when the smaller-ID element arrives
// before the cut is full and the larger-ID one arrives later.
func TestTieBoundaryLateArrivalLoses(t *testing.T) {
	s, err := New(2, Desc)
	if err != nil {
		t.Fatal(err)
	}
	s.Push("able", 5.0)
	s.Push("best", 10.0)
	s.Push("zulu", 5.0) // ties "able" at the boundary, must be rejected
	assertOrder(t, s, []string{"best", "able"})
}

// The snapshot must be independent of input arrival order: the same set of
// elements pushed in many shuffles yields element-for-element identical
// snapshots.
func TestSnapshotIndependentOfArrivalOrder(t *testing.T) {
	base := []Element{
		{ID: "e01", Score: 3.5},
		{ID: "e02", Score: 3.5},
		{ID: "e03", Score: 3.5},
		{ID: "e04", Score: -1.0},
		{ID: "e05", Score: 100.0},
		{ID: "e06", Score: 0.0},
		{ID: "e07", Score: 3.5},
		{ID: "e08", Score: -50.0},
		{ID: "e09", Score: 42.0},
		{ID: "e10", Score: 0.0},
	}
	for _, dir := range []Direction{Desc, Asc} {
		var want []Element
		rng := rand.New(rand.NewSource(42))
		for trial := 0; trial < 50; trial++ {
			perm := rng.Perm(len(base))
			s, err := New(4, dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, i := range perm {
				s.Push(base[i].ID, base[i].Score)
			}
			got := s.Snapshot()
			if want == nil {
				want = got
				continue
			}
			if len(got) != len(want) {
				t.Fatalf("dir=%v trial=%d: len=%d, want %d", dir, trial, len(got), len(want))
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("dir=%v trial=%d: snapshot[%d]=%v, want %v (full %v vs %v)",
						dir, trial, i, got[i], want[i], got, want)
				}
			}
		}
	}
}
