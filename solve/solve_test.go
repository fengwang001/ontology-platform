package solve

import (
	"errors"
	"fmt"
	"testing"

	"ontology/graph"
	"ontology/rng"
	"ontology/ver"
)

func mustAdd(t *testing.T, g *graph.Graph, pkg, v string, deps map[string]string) {
	t.Helper()
	if err := g.Add(pkg, v, deps); err != nil {
		t.Fatalf("Add(%s@%s): %v", pkg, v, err)
	}
}

func simpleGraph(t *testing.T) *graph.Graph {
	g := graph.New()
	mustAdd(t, g, "a", "1.0.0", map[string]string{"b": ">=1.2.0 <2.0.0"})
	mustAdd(t, g, "a", "2.0.0", map[string]string{"b": "<1.2.0"})
	mustAdd(t, g, "b", "1.1.0", nil)
	mustAdd(t, g, "b", "1.5.0", nil)
	mustAdd(t, g, "b", "2.0.0", nil)
	mustAdd(t, g, "z", "9.9.9", nil) // unreachable
	return g
}

func TestSolveBasic(t *testing.T) {
	s := New(simpleGraph(t))
	sol, err := s.Solve([]Root{{Pkg: "a", Spec: ">=1.0.0"}})
	if err != nil {
		t.Fatal(err)
	}
	if sol["a"] != "2.0.0" || sol["b"] != "1.1.0" {
		t.Fatalf("got %v want a=2.0.0 b=1.1.0", sol)
	}
	if _, ok := sol["z"]; ok {
		t.Fatalf("unreachable package z present: %v", sol)
	}
	if err := s.Verify([]Root{{Pkg: "a", Spec: ">=1.0.0"}}, sol); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(sol) != 2 {
		t.Fatalf("solution size=%d want 2", len(sol))
	}
}

func TestDeterminismAndOrderIndependence(t *testing.T) {
	roots := []Root{{Pkg: "a", Spec: ">=1.0.0"}}
	sol1, err := New(simpleGraph(t)).Solve(roots)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		got, err := New(simpleGraph(t)).Solve(roots)
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(got) != fmt.Sprint(sol1) {
			t.Fatalf("nondeterministic: %v vs %v", got, sol1)
		}
	}
}

func TestUnsolvableConflict(t *testing.T) {
	g := graph.New()
	mustAdd(t, g, "a", "1.0.0", map[string]string{"b": ">=2.0.0"})
	mustAdd(t, g, "b", "1.0.0", nil)
	_, err := New(g).Solve([]Root{{Pkg: "a", Spec: ">=1.0.0"}})
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("err=%v want *ConflictError", err)
	}
	if ce.Pkg != "b" || len(ce.Low) == 0 ||
		(!ce.NoCandidate && len(ce.High) == 0) {
		t.Fatalf("bad conflict: %+v", ce)
	}
	msg := err.Error()
	for _, want := range []string{"a@1.0.0 requires b >=2.0.0", "b"} {
		if !contains(msg, want) {
			t.Fatalf("conflict %q missing %q", msg, want)
		}
	}
}

func TestCyclicDependenciesResolve(t *testing.T) {
	g := graph.New()
	mustAdd(t, g, "a", "1.0.0", map[string]string{"b": ">=1.0.0"})
	mustAdd(t, g, "a", "2.0.0", map[string]string{"b": "<1.0.0"})
	mustAdd(t, g, "b", "1.0.0", map[string]string{"a": ">=1.0.0 <2.0.0"})
	sol, err := New(g).Solve([]Root{{Pkg: "a", Spec: ">=1.0.0"}})
	if err != nil {
		t.Fatalf("cycle must solve: %v", err)
	}
	if sol["a"] != "1.0.0" || sol["b"] != "1.0.0" {
		t.Fatalf("got %v", sol)
	}
}

func TestCyclicUnsolvableExplained(t *testing.T) {
	g := graph.New()
	mustAdd(t, g, "a", "1.0.0", map[string]string{"b": ">=2.0.0"})
	mustAdd(t, g, "b", "1.0.0", map[string]string{"a": ">=2.0.0"})
	_, err := New(g).Solve([]Root{{Pkg: "a", Spec: ">=1.0.0"}})
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("err=%v want *ConflictError", err)
	}
}

func TestDistinctErrors(t *testing.T) {
	cases := []struct {
		name string
		fn   func() error
		want error
	}{
		{"bad version", func() error {
			g := graph.New()
			return g.Add("a", "1.0", nil)
		}, ver.ErrSyntax},
		{"bad constraint", func() error {
			g := graph.New()
			return g.Add("a", "1.0.0", map[string]string{"b": "~1"})
		}, rng.ErrSyntax},
		{"unknown from root", func() error {
			_, err := New(graph.New()).Solve([]Root{{Pkg: "x", Spec: ">=1.0.0"}})
			return err
		}, nil},
		{"unknown from dep", func() error {
			g := graph.New()
			mustAdd(t, g, "a", "1.0.0", map[string]string{"missing": ">=1.0.0"})
			_, err := New(g).Solve([]Root{{Pkg: "a", Spec: ">=1.0.0"}})
			return err
		}, nil},
		{"duplicate", func() error {
			g := graph.New()
			_ = g.Add("a", "1.0.0", nil)
			return g.Add("a", "1.0.0", nil)
		}, graph.ErrDuplicateVersion},
	}
	for _, c := range cases {
		err := c.fn()
		if err == nil {
			t.Errorf("%s: want error", c.name)
			continue
		}
		if c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("%s: err=%v want %v", c.name, err, c.want)
		}
		if c.name == "unknown from root" || c.name == "unknown from dep" {
			var u *UnknownPackageError
			if !errors.As(err, &u) {
				t.Errorf("%s: err=%v want UnknownPackageError", c.name, err)
			}
		}
	}
}

func TestBudgetDistinctFromNoSolution(t *testing.T) {
	_, err := New(simpleGraph(t)).WithBudget(1).Solve(
		[]Root{{Pkg: "a", Spec: ">=1.0.0"}})
	if !errors.Is(err, ErrSearchBudget) {
		t.Fatalf("err=%v want ErrSearchBudget", err)
	}
	// Same problem without budget is a conflict, not a budget error.
	g2 := graph.New()
	mustAdd(t, g2, "a", "1.0.0", map[string]string{"b": ">=2.0.0"})
	mustAdd(t, g2, "b", "1.0.0", nil)
	_, err2 := New(g2).Solve([]Root{{Pkg: "a", Spec: ">=1.0.0"}})
	if errors.Is(err2, ErrSearchBudget) {
		t.Fatal("no-solution must not be reported as budget")
	}
	var ce *ConflictError
	if !errors.As(err2, &ce) {
		t.Fatalf("err2=%v want *ConflictError", err2)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
