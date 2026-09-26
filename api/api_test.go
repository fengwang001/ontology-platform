package api

import "errors"
import "math/rand"
import "reflect"
import "sync"
import "testing"
import "ontology/dag"

var notesEdges = [][2]int{{0, 1}, {0, 2}, {2, 1}, {3, 4}, {4, 5}}

func randDAG(rng *rand.Rand, n, p int) (es [][2]int) {
	for u := 0; u < n; u++ {
		for v := u + 1; v < n; v++ {
			if rng.Intn(p) == 0 {
				es = append(es, [2]int{u, v})
			}
		}
	}
	return es
}
func buildA(n int, es [][2]int, seed int64) *API {
	a, _ := New(n)
	for _, i := range rand.New(rand.NewSource(seed)).Perm(len(es)) { // shuffled order
		_ = a.AddEdge(es[i][0], es[i][1])
	}
	return a
}
func smallCases() []graphCase {
	cs := append([]graphCase(nil), builtinCases...)
	rng := rand.New(rand.NewSource(7))
	for s := 0; s < 16; s++ {
		n := 2 + rng.Intn(6) // n in [2,7]
		cs = append(cs, graphCase{n, randDAG(rng, n, 2)})
	}
	return cs
}
func edgeMap(es [][2]int) map[[2]int]bool {
	m := map[[2]int]bool{}
	for _, e := range es {
		m[e] = true
	}
	return m
}
func TestCoverLegality(t *testing.T) {
	want6 := [][]int{{0, 2, 1}, {3, 4, 5}}
	c6, p6, e6 := buildA(6, notesEdges, 1).Solve()
	c7, p7, e7 := buildA(7, notesEdges, 1).Solve() // isolated node 6
	if e6 != nil || e7 != nil || c6 != 2 || c7 != 3 || !reflect.DeepEqual(p6, want6) || !reflect.DeepEqual(p7[2], []int{6}) {
		t.Fatalf("section3: %d %v / %d %v (%v %v)", c6, p6, c7, p7, e6, e7)
	}
	for _, c := range smallCases() {
		for _, seed := range []int64{1, 2, 3} {
			a := buildA(c.n, c.edges, seed)
			cover, paths, err := a.Solve()
			if err != nil || legal(c.n, edgeMap(c.edges), cover, paths) != nil {
				t.Fatalf("%v seed=%d: %v", c, seed, err)
			}
		}
	}
	for _, n := range []int{16, 64, 200} {
		es := randDAG(rand.New(rand.NewSource(int64(n))), n, 3)
		cover, paths, err := buildA(n, es, 9).Solve()
		if err != nil || legal(n, edgeMap(es), cover, paths) != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
	}
}
func checkRefs(t *testing.T) {
	for _, c := range smallCases() {
		if c.n > 7 {
			continue
		}
		cover, _, err := buildA(c.n, c.edges, 1).Solve()
		_, adj := buildA(c.n, c.edges, 1).g.Snapshot()
		if err != nil || cover != c.n-bruteMatching(adj) || cover != bruteChains(c.n, edgeMap(c.edges)) {
			t.Fatalf("%v: cover=%d", c, cover)
		}
	}
}
func TestCoverMatchesFormula(t *testing.T) { checkRefs(t) }
func TestCoverMinimal(t *testing.T)        { checkRefs(t) }
func TestSentinelErrors(t *testing.T) {
	_, eN := New(0)
	a, _ := New(3)
	eR, eS := a.AddEdge(3, 0), a.AddEdge(1, 1)
	_ = a.AddEdge(0, 1)
	eDup := a.AddEdge(0, 1)
	_, _, eCyc := buildA(3, [][2]int{{0, 1}, {1, 2}, {2, 0}}, 1).Solve()
	got := []error{eN, eR, eS, eDup, eCyc}
	want := []error{dag.ErrInvalidN, dag.ErrNodeOutOfRange, dag.ErrSelfLoop, dag.ErrDuplicateEdge, dag.ErrCycle}
	seen := map[error]bool{}
	for i, e := range got {
		if !errors.Is(e, want[i]) {
			t.Fatalf("%d: got %v want %v", i, e, want[i])
		}
		seen[e] = true
	}
	if len(seen) != 5 {
		t.Fatalf("want 5 distinct sentinels, got %d", len(seen))
	}
}
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	a := buildA(6, notesEdges, 1)
	before := a.EdgeCount()
	_, wantP, _ := a.Solve()
	for _, ev := range [][2]int{{6, 0}, {-1, 0}, {0, 6}, {0, 0}, {0, 1}} {
		if a.AddEdge(ev[0], ev[1]) == nil {
			t.Fatalf("edge %v accepted", ev)
		}
	}
	c, p, err := a.Solve()
	if err != nil || a.EdgeCount() != before || c != 2 || !reflect.DeepEqual(p, wantP) {
		t.Fatalf("state changed: edges=%d cover=%d err=%v", a.EdgeCount(), c, err)
	}
	if a.AddEdge(1, 4) != nil || a.EdgeCount() != before+1 {
		t.Fatal("graph unusable after rejections")
	}
}
func TestConcurrentSolve(t *testing.T) {
	es := randDAG(rand.New(rand.NewSource(99)), 200, 3)
	a := buildA(200, es, 1)
	var wg sync.WaitGroup
	all := make([][][]int, 24)
	for i := range all {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var err error
			if _, all[i], err = a.Solve(); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	for i := 1; i < len(all); i++ {
		if !reflect.DeepEqual(all[i], all[0]) {
			t.Fatalf("goroutine %d differs", i)
		}
	}
}
func TestSelfCheck(t *testing.T) {
	if a, _ := New(1); a == nil {
		t.Fatal("New failed")
	} else if err := a.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
