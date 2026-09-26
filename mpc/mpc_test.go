package mpc

import (
	"math/rand"
	"testing"

	"ontology/dag"
)

// bruteMaxMatch independently maximises the bipartite matching by enumerating
// every subset of the given edges. Used only for tiny graphs in tests.
func bruteMaxMatch(edges [][2]int) int {
	best := 0
	for mask := 0; mask < 1<<len(edges); mask++ {
		l, r := map[int]bool{}, map[int]bool{}
		ok, size := true, 0
		for i, e := range edges {
			if mask>>uint(i)&1 == 0 {
				continue
			}
			if l[e[0]] || r[e[1]] {
				ok = false
				break
			}
			l[e[0]], r[e[1]], size = true, true, size+1
		}
		if ok && size > best {
			best = size
		}
	}
	return best
}

func randomDAG(rng *rand.Rand, n int) [][2]int {
	es := [][2]int{}
	for u := 0; u < n; u++ { // only u<v edges, so the graph is necessarily acyclic
		for v := u + 1; v < n; v++ {
			if rng.Intn(2) == 0 {
				es = append(es, [2]int{u, v})
			}
		}
	}
	rng.Shuffle(len(es), func(i, j int) { es[i], es[j] = es[j], es[i] })
	return es
}

func buildDAG(t *testing.T, n int, es [][2]int) *dag.Graph {
	t.Helper()
	g, err := dag.New(n)
	if err != nil {
		t.Fatalf("dag.New(%d): %v", n, err)
	}
	for _, e := range es {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			t.Fatalf("AddEdge(%v): %v", e, err)
		}
	}
	return g
}

// TestSolveExampleSix pins the section-3 derivation: cover 2 and exact paths.
func TestSolveExampleSix(t *testing.T) {
	es := [][2]int{{0, 1}, {0, 2}, {2, 1}, {3, 4}, {4, 5}}
	r, err := Solve(buildDAG(t, 6, es))
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if r.Cover != 2 || len(r.Paths) != 2 {
		t.Fatalf("cover=%d paths=%v, want 2 / [[0 2 1] [3 4 5]]", r.Cover, r.Paths)
	}
	want := [][]int{{0, 2, 1}, {3, 4, 5}}
	for i := range want {
		if len(r.Paths[i]) != len(want[i]) {
			t.Fatalf("path %d = %v, want %v", i, r.Paths[i], want[i])
		}
		for j := range want[i] {
			if r.Paths[i][j] != want[i][j] {
				t.Fatalf("path %d = %v, want %v", i, r.Paths, want)
			}
		}
	}
}

// TestCoverMatchesBruteForce checks invariants 2 and 3 value by value:
// cover must equal n-|M| for the independently enumerated maximum matching,
// across sizes and randomised edge insertion orders.
func TestCoverMatchesBruteForce(t *testing.T) {
	cases := []struct{ n, trials int }{
		{1, 1}, {2, 4}, {3, 8}, {4, 12}, {5, 16}, {6, 20}, {7, 12},
	}
	for _, c := range cases {
		for k := 0; k < c.trials; k++ {
			rng := rand.New(rand.NewSource(int64(c.n*1000 + k)))
			es := randomDAG(rng, c.n)
			if len(es) > 14 { // keep 2^|E| enumeration cheap for the reference
				es = es[:14]
			}
			r, err := Solve(buildDAG(t, c.n, es))
			if err != nil {
				t.Fatalf("n=%d Solve: %v", c.n, err)
			}
			want := c.n - bruteMaxMatch(es)
			if r.Cover != want {
				t.Fatalf("n=%d edges=%v cover=%d, want n-|M|=%d", c.n, es, r.Cover, want)
			}
		}
	}
}

// TestProbeCountConstant proves the "is this right node matched?" test is O(1):
// m left nodes are pre-matched to m distinct right nodes, then one final
// augmentation probes a single free right node. The probe count must stay <=1
// regardless of m (a full-table scan would grow linearly with m).
func TestProbeCountConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 5000, 10000} {
		n := m + 2
		g := buildDAG(t, n, nil)
		s := newSolver(g)
		for i := 0; i < m; i++ { // pre-match left i to right i
			s.adj[i] = []int{i}
			s.matchRight[i], s.matchLeft[i] = i, i
		}
		s.adj[m] = []int{m + 1} // free right node: one index lookup decides it
		s.tryAugment(m)
		if s.probeCount > 1 {
			t.Fatalf("m=%d probeCount=%d, want <= 1 (O(1) matchRight lookup)", m, s.probeCount)
		}
		if s.matchRight[m+1] != m {
			t.Fatalf("m=%d final augmentation did not match", m)
		}
	}
}
