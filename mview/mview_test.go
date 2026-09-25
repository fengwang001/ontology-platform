package mview

import (
	"fmt"
	"testing"

	"ontology/dep"
)

// chainState builds R0 -> C1 -> C2 (the affected chain) plus m unrelated
// base/view pairs U_i -> W_i that propagation must never visit.
func chainState(m int) *State {
	nodes := []dep.Node{
		{Name: "R0", Kind: dep.Base},
		{Name: "C1", Kind: dep.View, Deps: []string{"R0"}},
		{Name: "C2", Kind: dep.View, Deps: []string{"C1"}},
	}
	init := map[string]int{"R0": 0, "C1": 0, "C2": 0}
	exprs := map[string]Expr{
		"C1": func(v []int) int { return v[0] },
		"C2": func(v []int) int { return v[0] },
	}
	for i := 0; i < m; i++ {
		u := fmt.Sprintf("U%d", i)
		w := "W" + u
		nodes = append(nodes,
			dep.Node{Name: u, Kind: dep.Base},
			dep.Node{Name: w, Kind: dep.View, Deps: []string{u}})
		init[u], init[w] = 0, 0
		exprs[w] = func(v []int) int { return v[0] }
	}
	return New(dep.New(nodes...), init, exprs)
}

// TestPropagationVisitCount pins the non-exported propagation counter: it
// must equal the number of actually-affected dependent nodes and stay
// constant as m unrelated views are added (edge-bound, not full-graph scan).
func TestPropagationVisitCount(t *testing.T) {
	cases := []int{100, 1000, 10000}
	for _, m := range cases {
		s := chainState(m)
		if err := s.UpdateBase("R0", 1); err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		if s.lastVisit != 2 { // only C1 and C2 are reachable from R0
			t.Errorf("m=%d: chain update visited %d nodes, want 2", m, s.lastVisit)
		}
		// An unrelated base reaches exactly its one view, regardless of graph size.
		if err := s.UpdateBase("U0", 1); err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		if s.lastVisit != 1 {
			t.Errorf("m=%d: unrelated update visited %d nodes, want 1", m, s.lastVisit)
		}
	}
}

// TestFixedGraphVisitCount pins the count on the exercise's own diamond:
// changing B2 reaches V1, V2 and V3 exactly once each despite two paths.
func TestFixedGraphVisitCount(t *testing.T) {
	g := dep.New(
		dep.Node{Name: "B1", Kind: dep.Base},
		dep.Node{Name: "B2", Kind: dep.Base},
		dep.Node{Name: "B3", Kind: dep.Base},
		dep.Node{Name: "V1", Kind: dep.View, Deps: []string{"B1", "B2"}},
		dep.Node{Name: "V2", Kind: dep.View, Deps: []string{"B2", "B3"}},
		dep.Node{Name: "V3", Kind: dep.View, Deps: []string{"V1", "V2"}},
	)
	add := func(v []int) int { return v[0] + v[1] }
	s := New(g,
		map[string]int{"B1": 10, "B2": 20, "B3": 30, "V1": 30, "V2": 50, "V3": 80},
		map[string]Expr{"V1": add, "V2": add, "V3": add})
	want := map[string]int{"B1": 2, "B2": 3, "B3": 2}
	for b, n := range want {
		if err := s.UpdateBase(b, 1); err != nil {
			t.Fatal(err)
		}
		if s.lastVisit != n {
			t.Errorf("update %s visited %d, want %d", b, s.lastVisit, n)
		}
	}
}
