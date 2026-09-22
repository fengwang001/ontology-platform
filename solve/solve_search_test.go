package solve

import (
	"fmt"
	"sync"
	"testing"

	"ontology/graph"
)

func TestPruningFarBelowBruteForce(t *testing.T) {
	g := graph.New()
	for i := 0; i < 10; i++ {
		p := fmt.Sprintf("p%d", i)
		for k := 1; k <= 10; k++ {
			var deps []graph.Dep
			if i > 0 {
				deps = append(deps, graph.Dep{
					Target:     fmt.Sprintf("p%d", i-1),
					Constraint: fmt.Sprintf(">=%d.0.0", k),
				})
			}
			add(t, g, p, fmt.Sprintf("%d.0.0", k), deps...)
		}
	}
	sol, err := New(g).Solve([]Root{{Package: "p9", Constraint: "*"}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	v, _ := sol.VersionOf("p0")
	if v.String() != "10.0.0" {
		t.Fatalf("unique solution expected p0=10.0.0, got %s", v)
	}
	if sol.Attempts() >= 10000 {
		t.Fatalf("attempts=%d, expected pruning far below 10^10", sol.Attempts())
	}
	t.Logf("attempts=%d (limit 10000)", sol.Attempts())
}

func TestConcurrentSolveIdentical(t *testing.T) {
	g := graph.New()
	add(t, g, "a", "1.0.0", graph.Dep{Target: "b", Constraint: ">=1.0.0"})
	add(t, g, "a", "2.0.0", graph.Dep{Target: "b", Constraint: ">=2.0.0"})
	for k := 1; k <= 5; k++ {
		add(t, g, "b", fmt.Sprintf("%d.0.0", k))
	}
	s := New(g)
	roots := []Root{{Package: "a", Constraint: "*"}}
	const M = 32
	var wg sync.WaitGroup
	results := make([]string, M)
	wg.Add(M)
	for i := 0; i < M; i++ {
		go func(i int) {
			defer wg.Done()
			sol, err := s.Solve(roots, Options{})
			if err != nil {
				t.Errorf("solve: %v", err)
				return
			}
			if err := s.Validate(sol, roots); err != nil {
				t.Errorf("validate: %v", err)
				return
			}
			var out string
			for _, c := range sol.Choices() {
				out += c.Package + "=" + c.Version.String() + ";"
			}
			results[i] = out
		}(i)
	}
	wg.Wait()
	for i := 1; i < M; i++ {
		if results[i] != results[0] {
			t.Fatalf("concurrent results differ: %q vs %q", results[0], results[i])
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}())
}
