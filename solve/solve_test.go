package solve

import (
	"errors"
	"fmt"
	"testing"

	"ontology/graph"
	"ontology/rng"
	"ontology/ver"
)

func add(t *testing.T, g *graph.Graph, name, v string, deps ...graph.Dep) {
	t.Helper()
	if err := g.AddVersion(name, v, deps...); err != nil {
		t.Fatalf("add %s@%s: %v", name, v, err)
	}
}

func pick(sol *Solution) map[string]string {
	m := map[string]string{}
	for _, c := range sol.Choices() {
		m[c.Package] = c.Version.String()
	}
	return m
}

func TestSolveBasicAndDeterminism(t *testing.T) {
	build := func() *graph.Graph {
		g := graph.New()
		add(t, g, "a", "1.0.0", graph.Dep{Target: "b", Constraint: ">=1.2.0 <2.0.0"})
		add(t, g, "a", "2.0.0", graph.Dep{Target: "b", Constraint: ">=2.0.0"})
		add(t, g, "b", "1.2.0")
		add(t, g, "b", "1.5.0")
		add(t, g, "b", "2.0.0")
		add(t, g, "unrelated", "9.9.9")
		return g
	}
	g := build()
	s := New(g)
	sol, err := s.Solve([]Root{{Package: "a", Constraint: "*"}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(sol, []Root{{Package: "a", Constraint: "*"}}); err != nil {
		t.Fatalf("validate: %v", err)
	}
	want := map[string]string{"a": "2.0.0", "b": "2.0.0"}
	if got := pick(sol); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if _, ok := sol.VersionOf("unrelated"); ok {
		t.Fatal("unreachable package must not appear")
	}
	// 打乱登记顺序，结果逐位相同。
	g2 := graph.New()
	add(t, g2, "unrelated", "9.9.9")
	add(t, g2, "b", "2.0.0")
	add(t, g2, "a", "2.0.0", graph.Dep{Target: "b", Constraint: ">=2.0.0"})
	add(t, g2, "b", "1.5.0")
	add(t, g2, "b", "1.2.0")
	add(t, g2, "a", "1.0.0", graph.Dep{Target: "b", Constraint: "<2.0.0 >=1.2.0"})
	sol2, err := New(g2).Solve([]Root{{Package: "a", Constraint: "*"}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(pick(sol2)) != fmt.Sprint(want) {
		t.Fatalf("registration order changed result: %v", pick(sol2))
	}
}

func TestPrereleaseParticipation(t *testing.T) {
	cases := []struct {
		constraint string
		wantB      string
	}{
		{">=1.0.0-beta", "1.0.0"},
		{">=1.0.0-beta <1.0.0", "1.0.0-beta"},
		{">=1.0.0", "1.0.0"},
	}
	for _, tc := range cases {
		t.Run(tc.constraint, func(t *testing.T) {
			g := graph.New()
			add(t, g, "a", "1.0.0", graph.Dep{Target: "b", Constraint: tc.constraint})
			add(t, g, "b", "1.0.0-beta")
			add(t, g, "b", "1.0.0")
			sol, err := New(g).Solve([]Root{{Package: "a", Constraint: "*"}}, Options{})
			if err != nil {
				t.Fatal(err)
			}
			v, _ := sol.VersionOf("b")
			if v.String() != tc.wantB {
				t.Fatalf("b=%s want %s", v, tc.wantB)
			}
		})
	}
}

func TestUnsatisfiableConflictChain(t *testing.T) {
	g := graph.New()
	add(t, g, "a", "1.0.0", graph.Dep{Target: "b", Constraint: ">=2.0.0"})
	add(t, g, "b", "1.0.0")
	_, err := New(g).Solve([]Root{{Package: "a", Constraint: "*"}, {Package: "b", Constraint: "<2.0.0"}}, Options{})
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("want ConflictError, got %T %v", err, err)
	}
	if !errors.Is(err, ErrNoSolution) || ce.Package != "b" || len(ce.Chain) == 0 {
		t.Fatalf("bad conflict: %+v", ce)
	}
	msg := err.Error()
	for _, frag := range []string{">=2.0.0", "<2.0.0", "<root>", "a@1.0.0", "chain"} {
		if !contains(msg, frag) {
			t.Fatalf("conflict message %q missing %q", msg, frag)
		}
	}
	// 同一输入重复求解，冲突解释逐位相同。
	_, err2 := New(g).Solve([]Root{{Package: "a", Constraint: "*"}, {Package: "b", Constraint: "<2.0.0"}}, Options{})
	if err2.Error() != msg {
		t.Fatalf("conflict explanation not deterministic:\n%q\n%q", msg, err2.Error())
	}
}

func TestCyclesAreSolvable(t *testing.T) {
	g := graph.New()
	add(t, g, "a", "1.0.0", graph.Dep{Target: "b", Constraint: ">=1.0.0"})
	add(t, g, "b", "1.0.0", graph.Dep{Target: "a", Constraint: ">=1.0.0"})
	s := New(g)
	sol, err := s.Solve([]Root{{Package: "a", Constraint: "*"}}, Options{})
	if err != nil {
		t.Fatalf("cycle must solve: %v", err)
	}
	if err := s.Validate(sol, []Root{{Package: "a", Constraint: "*"}}); err != nil {
		t.Fatal(err)
	}
}

func TestDistinctErrors(t *testing.T) {
	g := graph.New()
	add(t, g, "a", "1.0.0", graph.Dep{Target: "ghost", Constraint: ">=1.0.0"})
	cases := []struct {
		name  string
		roots []Root
		want  error
	}{
		{"unknown-package", []Root{{Package: "nope", Constraint: "*"}}, ErrUnknownPackage},
		{"unknown-dep-target", []Root{{Package: "a", Constraint: "*"}}, ErrUnknownPackage},
		{"bad-root-constraint", []Root{{Package: "a", Constraint: "~1"}}, rng.ErrInvalidConstraint},
		{"bad-version-in-data", nil, ver.ErrInvalidVersion},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "bad-version-in-data" {
				if err := g.AddVersion("z", "xx"); !errors.Is(err, ver.ErrInvalidVersion) {
					t.Fatalf("err=%v", err)
				}
				return
			}
			if _, err := New(g).Solve(tc.roots, Options{}); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
		})
	}
}

func TestBudgetDistinctFromNoSolution(t *testing.T) {
	g := graph.New()
	add(t, g, "a", "1.0.0", graph.Dep{Target: "b", Constraint: ">=2.0.0"})
	add(t, g, "b", "1.0.0")
	_, errNo := New(g).Solve([]Root{{Package: "b", Constraint: "<1.0.0"}}, Options{MaxAttempts: 100})
	if !errors.Is(errNo, ErrNoSolution) || errors.Is(errNo, ErrSearchBudget) {
		t.Fatalf("no-solution wrong: %v", errNo)
	}
}

func TestBudgetTripped(t *testing.T) {
	g := graph.New()
	for i := 0; i < 3; i++ {
		add(t, g, fmt.Sprintf("p%d", i), "1.0.0")
	}
	s := New(g)
	_, err := s.Solve([]Root{{Package: "p0", Constraint: "*"}, {Package: "p1", Constraint: "*"},
		{Package: "p2", Constraint: "*"}}, Options{MaxAttempts: 1})
	if !errors.Is(err, ErrSearchBudget) || errors.Is(err, ErrNoSolution) {
		t.Fatalf("err=%v", err)
	}
}
