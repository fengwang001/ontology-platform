package topk

import (
	"fmt"
	"math/rand"
	"testing"
)

func idsOf(items []Element) []string {
	ids := make([]string, len(items))
	for i, e := range items {
		ids[i] = e.ID
	}
	return ids
}

func equalSnapshot(a, b []Element) bool {
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

func mustNew(t *testing.T, k int, dir Direction) *Selector {
	t.Helper()
	s, err := New(k, dir)
	if err != nil {
		t.Fatalf("New(%d, %v): %v", k, dir, err)
	}
	return s
}

// Ties must break by ascending ID in both directions; the direction must
// never flip the ID tie-break into descending.
func TestTieBreakByIDAscBothDirections(t *testing.T) {
	want := []string{"a", "b", "c"}
	for _, dir := range []Direction{Desc, Asc} {
		s := mustNew(t, 3, dir)
		s.Push("c", 7)
		s.Push("a", 7)
		s.Push("b", 7)
		got := idsOf(s.Snapshot())
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("dir=%v: got ids %v, want %v", dir, got, want)
			}
		}
	}
}

// Direction applies to the score dimension only.
func TestDirectionOrdersScores(t *testing.T) {
	desc := mustNew(t, 2, Desc)
	asc := mustNew(t, 2, Asc)
	for _, e := range []Element{{"low", 1}, {"mid", 5}, {"high", 9}} {
		desc.Push(e.ID, e.Score)
		asc.Push(e.ID, e.Score)
	}
	if got := idsOf(desc.Snapshot()); got[0] != "high" || got[1] != "mid" {
		t.Fatalf("Desc snapshot ids = %v, want [high mid]", got)
	}
	if got := idsOf(asc.Snapshot()); got[0] != "low" || got[1] != "mid" {
		t.Fatalf("Asc snapshot ids = %v, want [low mid]", got)
	}
}

// A tie straddling the K/K+1 boundary must keep the lexicographically
// smaller ID, not the one that arrived first.
func TestTieAcrossBoundaryKeepsSmallerID(t *testing.T) {
	for _, dir := range []Direction{Desc, Asc} {
		s := mustNew(t, 2, dir)
		s.Push("zeta", 10) // arrives first but loses the tie
		s.Push("alpha", 10)
		s.Push("mid", 10)
		got := idsOf(s.Snapshot())
		if len(got) != 2 || got[0] != "alpha" || got[1] != "mid" {
			t.Fatalf("dir=%v: got ids %v, want [alpha mid]", dir, got)
		}
	}
}

// The snapshot must be fully independent of arrival order: the same
// elements pushed in any shuffled order yield identical sequences.
func TestShuffleInvariance(t *testing.T) {
	base := make([]Element, 0, 64)
	for i := 0; i < 64; i++ {
		// Deliberately reuse scores to create many ties, including
		// ties straddling the K boundary.
		base = append(base, Element{
			ID:    fmt.Sprintf("id-%02d", i),
			Score: float64(i%8) - 4,
		})
	}
	for _, dir := range []Direction{Desc, Asc} {
		var want []Element
		for seed := int64(0); seed < 12; seed++ {
			shuffled := append([]Element(nil), base...)
			rand.New(rand.NewSource(seed)).Shuffle(len(shuffled), func(i, j int) {
				shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
			})
			s := mustNew(t, 10, dir)
			for _, e := range shuffled {
				s.Push(e.ID, e.Score)
			}
			got := s.Snapshot()
			if seed == 0 {
				want = got
				continue
			}
			if !equalSnapshot(want, got) {
				t.Fatalf("dir=%v seed=%d: snapshot %v differs from %v",
					dir, seed, got, want)
			}
		}
	}
}
