package solve_test

import (
	"errors"
	"strings"
	"testing"

	"ontology/graph"
	"ontology/solve"
)

type dep struct{ pkg, ver, target, con string }

func buildGraph(t *testing.T, versions map[string][]string, deps []dep) *graph.Graph {
	t.Helper()
	g := graph.New()
	for pkg, vs := range versions {
		for _, v := range vs {
			if err := g.AddVersion(pkg, v); err != nil {
				t.Fatalf("AddVersion(%s,%s): %v", pkg, v, err)
			}
		}
	}
	for _, d := range deps {
		if err := g.AddConstraint(d.pkg, d.ver, d.target, d.con); err != nil {
			t.Fatalf("AddConstraint(%v): %v", d, err)
		}
	}
	return g
}

func TestSolveScenarios(t *testing.T) {
	cases := []struct {
		name     string
		versions map[string][]string
		deps     []dep
		roots    []solve.Requirement
		want     string // canonical Solution.String(); "" means expect failure
	}{
		{
			name:     "highest satisfying version wins",
			versions: map[string][]string{"a": {"1.0.0", "2.0.0", "1.5.0"}},
			roots:    []solve.Requirement{{Package: "a", Constraint: ">=1.0.0"}},
			want:     "a@2.0.0",
		},
		{
			name:     "dependency chain",
			versions: map[string][]string{"a": {"1.0.0"}, "b": {"1.0.0", "2.0.0"}},
			deps:     []dep{{"a", "1.0.0", "b", "<2.0.0"}},
			roots:    []solve.Requirement{{Package: "a", Constraint: ">=1.0.0"}},
			want:     "a@1.0.0\nb@1.0.0",
		},
		{
			name:     "prerelease below release for same triple",
			versions: map[string][]string{"a": {"1.0.0-beta", "1.0.0"}},
			roots:    []solve.Requirement{{Package: "a", Constraint: ">=1.0.0"}},
			want:     "a@1.0.0",
		},
		{
			name:     "prerelease excluded by >=release",
			versions: map[string][]string{"a": {"1.0.0-beta"}},
			roots:    []solve.Requirement{{Package: "a", Constraint: ">=1.0.0"}},
			want:     "",
		},
		{
			name:     "unreachable package excluded",
			versions: map[string][]string{"a": {"1.0.0"}, "zz": {"9.9.9"}},
			roots:    []solve.Requirement{{Package: "a", Constraint: ">=1.0.0"}},
			want:     "a@1.0.0",
		},
		{
			name:     "circular dependency solvable",
			versions: map[string][]string{"a": {"1.0.0"}, "b": {"1.0.0"}},
			deps: []dep{
				{"a", "1.0.0", "b", ">=1.0.0"},
				{"b", "1.0.0", "a", ">=1.0.0"},
			},
			roots: []solve.Requirement{{Package: "a", Constraint: ">=1.0.0"}},
			want:  "a@1.0.0\nb@1.0.0",
		},
		{
			name:     "backtrack to lower version",
			versions: map[string][]string{"a": {"1.0.0", "2.0.0"}, "b": {"1.0.0"}},
			deps: []dep{
				{"a", "2.0.0", "b", ">=2.0.0"},
				{"a", "1.0.0", "b", ">=1.0.0"},
			},
			roots: []solve.Requirement{{Package: "a", Constraint: ">=1.0.0"}},
			want:  "a@1.0.0\nb@1.0.0",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := buildGraph(t, c.versions, c.deps)
			sol, err := solve.New(g).Solve(c.roots)
			if c.want == "" {
				if !errors.Is(err, solve.ErrNoSolution) {
					t.Fatalf("err=%v, want ErrNoSolution", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Solve: %v", err)
			}
			if got := sol.String(); got != c.want {
				t.Fatalf("solution:\n%s\nwant:\n%s", got, c.want)
			}
			if err := solve.Validate(g, c.roots, sol); err != nil {
				t.Fatalf("Validate: %v", err)
			}
		})
	}
}

func TestDeterminism(t *testing.T) {
	versions := map[string][]string{
		"a": {"1.0.0", "2.0.0"},
		"b": {"1.0.0", "1.5.0", "2.0.0"},
		"c": {"0.1.0", "0.2.0"},
	}
	deps := []dep{
		{"a", "2.0.0", "b", ">=1.5.0"},
		{"a", "1.0.0", "b", "<1.5.0"},
		{"b", "2.0.0", "c", ">=0.2.0"},
		{"b", "1.5.0", "c", "<0.2.0"},
		{"b", "1.0.0", "c", "<0.2.0"},
	}
	roots := []solve.Requirement{{Package: "a", Constraint: ">=1.0.0"}}
	base := buildGraph(t, versions, deps)
	first, err := solve.New(base).Solve(roots)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		sol, err := solve.New(base).Solve(roots)
		if err != nil {
			t.Fatal(err)
		}
		if sol.String() != first.String() {
			t.Fatalf("run %d differs:\n%s\nvs\n%s", i, sol, first)
		}
	}
	shuffled := buildGraph(t, map[string][]string{
		"c": {"0.2.0", "0.1.0"},
		"b": {"2.0.0", "1.0.0", "1.5.0"},
		"a": {"2.0.0", "1.0.0"},
	}, []dep{
		{"b", "1.0.0", "c", "<0.2.0"},
		{"b", "1.5.0", "c", "<0.2.0"},
		{"b", "2.0.0", "c", ">=0.2.0"},
		{"a", "1.0.0", "b", "<1.5.0"},
		{"a", "2.0.0", "b", ">=1.5.0"},
	})
	sol, err := solve.New(shuffled).Solve(roots)
	if err != nil {
		t.Fatal(err)
	}
	if sol.String() != first.String() {
		t.Fatalf("shuffled registration changed result:\n%s\nvs\n%s", sol, first)
	}
}

func TestConflictExplanation(t *testing.T) {
	g := buildGraph(t,
		map[string][]string{"app": {"1.0.0"}, "lib": {"1.0.0", "3.0.0"}},
		[]dep{{"app", "1.0.0", "lib", ">=2.0.0"}},
	)
	roots := []solve.Requirement{{Package: "app", Constraint: ">=1.0.0"}, {Package: "lib", Constraint: "<2.0.0"}}
	var prev string
	for i := 0; i < 3; i++ {
		_, err := solve.New(g).Solve(roots)
		var ce *solve.ConflictError
		if !errors.As(err, &ce) {
			t.Fatalf("err=%v, want *ConflictError", err)
		}
		if !errors.Is(err, solve.ErrNoSolution) || errors.Is(err, solve.ErrBudgetExceeded) {
			t.Fatalf("error classification wrong: %v", err)
		}
		for _, want := range []string{
			"root requires app >=1.0.0",
			"app@1.0.0 requires lib >=2.0.0",
			"root requires lib <2.0.0",
			"incompatible",
		} {
			if !strings.Contains(ce.Chain, want) {
				t.Errorf("chain missing %q:\n%s", want, ce.Chain)
			}
		}
		if prev != "" && ce.Chain != prev {
			t.Fatalf("explanation not deterministic:\n%s\nvs\n%s", ce.Chain, prev)
		}
		prev = ce.Chain
	}
}
