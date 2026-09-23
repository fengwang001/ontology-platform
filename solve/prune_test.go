package solve_test

import (
	"fmt"
	"testing"

	"ontology/graph"
	"ontology/solve"
)

// TestPruning builds 10 packages with 10 versions each (10^10 naive
// combinations) where exactly one assignment works — every package at
// 0.0.0, i.e. NOT on the first path of naive descending search: every
// p_i@v_j with j>=1 imposes an unsatisfiable constraint on the next
// package. Forward checking kills each bad branch after a single
// attempt, so the total number of tried package@version combinations
// must stay far below the brute-force 10^10.
func TestPruning(t *testing.T) {
	const nPackages, nVersions = 10, 10
	g := graph.New()
	name := func(i int) string { return fmt.Sprintf("p%d", i) }
	version := func(j int) string { return fmt.Sprintf("0.0.%d", j) }
	for i := 0; i < nPackages; i++ {
		for j := 0; j < nVersions; j++ {
			if err := g.AddVersion(name(i), version(j)); err != nil {
				t.Fatal(err)
			}
		}
	}
	for i := 0; i < nPackages; i++ {
		next := name((i + 1) % nPackages)
		for j := 0; j < nVersions; j++ {
			con := ">=0.0.0"
			if j >= 1 {
				con = "<0.0.0" // unsatisfiable: kills the branch at once
			}
			if err := g.AddConstraint(name(i), version(j), next, con); err != nil {
				t.Fatal(err)
			}
		}
	}
	roots := []solve.Requirement{{Package: "p0", Constraint: ">=0.0.0"}}
	s := solve.New(g)
	sol, err := s.Solve(roots)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if got := len(sol); got != nPackages {
		t.Fatalf("solution covers %d packages, want %d", got, nPackages)
	}
	for i := 0; i < nPackages; i++ {
		if v := sol[name(i)].String(); v != "0.0.0" {
			t.Fatalf("sol[%s]=%s, want 0.0.0 (the unique solution)", name(i), v)
		}
	}
	if err := solve.Validate(g, roots, sol); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	// 9 failing candidates + 1 success per package = 100 attempts;
	// brute force would be 10^10. The asserted bound is 10000.
	const bound = 10000
	if got := s.Attempts(); got >= bound {
		t.Fatalf("attempts=%d, want < %d (brute force is 10^10)", got, bound)
	} else {
		t.Logf("attempts=%d (bound %d, brute force 10000000000)", got, bound)
	}
}
