package solve

import (
	"fmt"
	"sync"
	"testing"

	"ontology/graph"
)

// buildPruneGraph: 10 packages p0..p8 have 10 versions 1.0.0..10.0.0, and
// every version N pins the next package to exactly N.0.0. The terminal p9
// only exists as 1.0.0, so the unique solution is all 1.0.0 even though the
// descending search starts from 10.0.0. Propagation must prune each wrong
// prefix immediately at p9 rather than enumerating all 10^10 combinations.
func buildPruneGraph(t *testing.T) *graph.Graph {
	t.Helper()
	g := graph.New()
	for i := 0; i < 10; i++ {
		pkg := fmt.Sprintf("p%d", i)
		max := 10
		if i == 9 {
			max = 1
		}
		for n := 1; n <= max; n++ {
			deps := map[string]string{}
			if i < 9 {
				next := fmt.Sprintf("p%d", i+1)
				deps[next] = fmt.Sprintf("=%d.0.0", n)
			}
			if err := g.Add(pkg, fmt.Sprintf("%d.0.0", n), deps); err != nil {
				t.Fatal(err)
			}
		}
	}
	return g
}

func TestPruningTriesFarBelowExhaustive(t *testing.T) {
	const limit = 10000
	g := buildPruneGraph(t)
	s := New(g)
	sol, err := s.Solve([]Root{{Pkg: "p0", Spec: ">=1.0.0"}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if sol[fmt.Sprintf("p%d", i)] != "1.0.0" {
			t.Fatalf("p%d=%s want 1.0.0; sol=%v", i,
				sol[fmt.Sprintf("p%d", i)], sol)
		}
	}
	tried := s.Tried()
	if tried >= limit {
		t.Fatalf("tried=%d must be far below %d (exhaustive is 10^10)", tried, limit)
	}
	t.Logf("prune graph: tried=%d combinations (< %d)", tried, limit)
	if err := s.Verify([]Root{{Pkg: "p0", Spec: ">=1.0.0"}}, sol); err != nil {
		t.Fatal(err)
	}
}

func TestRegistrationOrderIndependence(t *testing.T) {
	g1 := buildPruneGraph(t)

	// Identical data registered in reverse package and version order.
	g2 := graph.New()
	for i := 9; i >= 0; i-- {
		pkg := fmt.Sprintf("p%d", i)
		max := 10
		if i == 9 {
			max = 1
		}
		for n := max; n >= 1; n-- {
			deps := map[string]string{}
			if i < 9 {
				next := fmt.Sprintf("p%d", i+1)
				deps[next] = fmt.Sprintf("=%d.0.0", n)
			}
			if err := g2.Add(pkg, fmt.Sprintf("%d.0.0", n), deps); err != nil {
				t.Fatal(err)
			}
		}
	}
	roots := []Root{{Pkg: "p0", Spec: ">=1.0.0"}}
	s1, err := New(g1).Solve(roots)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := New(g2).Solve(roots)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(s1) != fmt.Sprint(s2) {
		t.Fatalf("order dependence:\n%v\n%v", s1, s2)
	}
}

func TestConcurrentSolve(t *testing.T) {
	g := buildPruneGraph(t)
	s := New(g)
	roots := []Root{{Pkg: "p0", Spec: ">=1.0.0"}}

	ref, err := s.Solve(roots)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprint(ref)

	const m = 16
	var wg sync.WaitGroup
	errs := make(chan error, m)
	wg.Add(m)
	for i := 0; i < m; i++ {
		go func() {
			defer wg.Done()
			got, err := s.Solve(roots)
			if err != nil {
				errs <- err
				return
			}
			if fmt.Sprint(got) != want {
				errs <- fmt.Errorf("mismatch: %v", got)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
