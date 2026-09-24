package semi

import (
	"fmt"
	"testing"
)

func chainEdges(m int) [][2]string {
	e := make([][2]string, m)
	for i := 1; i <= m; i++ {
		e[i-1] = [2]string{fmt.Sprintf("a%d", i), fmt.Sprintf("a%d", i+1)}
	}
	return e
}

var cycleEdges = [][2]string{{"a", "b"}, {"b", "c"}, {"c", "d"}, {"b", "d"}, {"d", "e"}, {"e", "b"}}

// TestCandidateCounterChain pins the complexity bound: on a chain of m
// edges the total number of join candidates (unexported counter) is exactly
// m(m-1)/2 — every derived tuple is joined exactly once. Re-joining the
// full path each round would cost a super-linear, ~m^3, amount.
func TestCandidateCounterChain(t *testing.T) {
	for _, m := range []int{100, 500, 2000, 10000} {
		e := New(chainEdges(m))
		e.Eval()
		if want := m * (m - 1) / 2; e.cand != want {
			t.Errorf("m=%d: candidates=%d, want exactly %d", m, e.cand, want)
		}
		if got, want := e.path.Size(), m*(m+1)/2; got != want {
			t.Errorf("m=%d: path size=%d, want %d", m, got, want)
		}
	}
}

// TestRoundsTrace nails the worked example of NOTES.md: the edge set with
// the cycle b->c->d->e->b must reproduce the exact five-round trace.
func TestRoundsTrace(t *testing.T) {
	e := New(cycleEdges)
	e.Eval()
	want := []Round{
		{Candidates: 0, Added: 6, PathSize: 6},
		{Candidates: 8, Added: 7, PathSize: 13},
		{Candidates: 8, Added: 6, PathSize: 19},
		{Candidates: 8, Added: 1, PathSize: 20},
		{Candidates: 1, Added: 0, PathSize: 20},
	}
	got := e.Rounds()
	if len(got) != len(want) {
		t.Fatalf("rounds=%d, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Candidates != w.Candidates || got[i].Added != w.Added || got[i].PathSize != w.PathSize {
			t.Errorf("round %d: got %+v, want %+v", i, got[i], w)
		}
		if len(got[i].Delta) != w.Added {
			t.Errorf("round %d: len(Delta)=%d, want %d", i, len(got[i].Delta), w.Added)
		}
	}
}

// TestFixpointLastDeltaEmpty: evaluation terminates and the last recorded
// round always has an empty Delta.
func TestFixpointLastDeltaEmpty(t *testing.T) {
	edgesOf := map[string][][2]string{"cycle6": cycleEdges, "chain50": chainEdges(50), "empty": {}}
	roundsOf := map[string]int{"cycle6": 5, "chain50": 51, "empty": 1}
	for name, want := range roundsOf {
		e := New(edgesOf[name])
		e.Eval()
		rounds := e.Rounds()
		if len(rounds[len(rounds)-1].Delta) != 0 {
			t.Errorf("%s: last delta not empty", name)
		}
		if len(rounds) != want {
			t.Errorf("%s: %d rounds, want %d", name, len(rounds), want)
		}
	}
}

// TestDeltaExactlyOnce: every derived tuple enters Delta in exactly one
// round, and the union of all Deltas is exactly path.
func TestDeltaExactlyOnce(t *testing.T) {
	for name, edges := range map[string][][2]string{"cycle6": cycleEdges, "chain50": chainEdges(50)} {
		e := New(edges)
		e.Eval()
		seen := map[[2]string]bool{}
		for _, r := range e.Rounds() {
			for _, p := range r.Delta {
				if seen[p] {
					t.Fatalf("%s: %v re-entered delta", name, p)
				}
				seen[p] = true
			}
		}
		if len(seen) != len(e.Path()) {
			t.Errorf("%s: delta union != path", name)
		}
	}
}
