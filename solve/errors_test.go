package solve_test

import (
	"errors"
	"testing"

	"ontology/graph"
	"ontology/rng"
	"ontology/solve"
	"ontology/ver"
)

func TestDecodableErrors(t *testing.T) {
	newGraph := func() *graph.Graph {
		return buildGraph(t, map[string][]string{"a": {"1.0.0"}}, nil)
	}
	cases := []struct {
		name string
		run  func() error
		want error
	}{
		{
			name: "invalid version string",
			run:  func() error { return newGraph().AddVersion("a", "1.0") },
			want: ver.ErrInvalidVersion,
		},
		{
			name: "invalid constraint string",
			run:  func() error { return newGraph().AddConstraint("a", "1.0.0", "b", ">>1") },
			want: rng.ErrInvalidConstraint,
		},
		{
			name: "invalid root constraint string",
			run: func() error {
				_, err := solve.New(newGraph()).Solve([]solve.Requirement{{Package: "a", Constraint: ">>1"}})
				return err
			},
			want: rng.ErrInvalidConstraint,
		},
		{
			name: "duplicate registration",
			run:  func() error { return newGraph().AddVersion("a", "1.0.0") },
			want: graph.ErrDuplicateVersion,
		},
		{
			name: "root references unregistered package",
			run: func() error {
				_, err := solve.New(newGraph()).Solve([]solve.Requirement{{Package: "ghost", Constraint: ">=1.0.0"}})
				return err
			},
			want: solve.ErrUnknownPackage,
		},
		{
			name: "constraint references unregistered package",
			run: func() error {
				g := buildGraph(t, map[string][]string{"a": {"1.0.0"}},
					[]dep{{"a", "1.0.0", "ghost", ">=1.0.0"}})
				_, err := solve.New(g).Solve([]solve.Requirement{{Package: "a", Constraint: ">=1.0.0"}})
				return err
			},
			want: solve.ErrUnknownPackage,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.run()
			if !errors.Is(err, c.want) {
				t.Fatalf("err=%v, want %v", err, c.want)
			}
			// The four decidable error kinds are mutually distinct.
			for _, other := range []error{
				ver.ErrInvalidVersion, rng.ErrInvalidConstraint,
				graph.ErrDuplicateVersion, solve.ErrUnknownPackage,
			} {
				if other != c.want && errors.Is(err, other) {
					t.Errorf("err=%v unexpectedly matches %v", err, other)
				}
			}
		})
	}
}

func TestBudgetVsNoSolution(t *testing.T) {
	// Overlapping but never-empty constraints keep the search branching;
	// a budget of 1 must stop it long before exhaustion.
	g := buildGraph(t,
		map[string][]string{"a": {"1.0.0", "2.0.0"}, "b": {"1.0.0", "2.0.0"}},
		[]dep{
			{"a", "1.0.0", "b", ">=1.0.0"},
			{"a", "2.0.0", "b", ">=1.0.0"},
		},
	)
	roots := []solve.Requirement{{Package: "a", Constraint: ">=1.0.0"}}
	_, err := solve.New(g, solve.WithMaxAttempts(1)).Solve(roots)
	if !errors.Is(err, solve.ErrBudgetExceeded) {
		t.Fatalf("err=%v, want ErrBudgetExceeded", err)
	}
	if errors.Is(err, solve.ErrNoSolution) {
		t.Fatal("budget exhaustion must not classify as no-solution")
	}
	// Same input without the tight budget is solvable: the two outcomes
	// are genuinely different conditions.
	if _, err := solve.New(g).Solve(roots); err != nil {
		t.Fatalf("unbudgeted solve should succeed: %v", err)
	}
}

func TestCircularUnsatisfiable(t *testing.T) {
	g := buildGraph(t,
		map[string][]string{"a": {"1.0.0"}, "b": {"2.0.0"}},
		[]dep{
			{"a", "1.0.0", "b", ">=2.0.0"},
			{"b", "2.0.0", "a", ">=2.0.0"}, // a has no such version
		},
	)
	_, err := solve.New(g).Solve([]solve.Requirement{{Package: "a", Constraint: ">=1.0.0"}})
	var ce *solve.ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("err=%v, want *ConflictError (cycle must terminate)", err)
	}
	if ce.Chain == "" {
		t.Fatal("conflict chain must not be empty")
	}
}
