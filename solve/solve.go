// Package solve finds, for a set of root requirements, an assignment of
// exactly one version per reachable package that satisfies every active
// constraint, or explains the conflict when none exists.
package solve

import (
	"errors"
	"fmt"
	"sort"
	"sync/atomic"

	"ontology/graph"
	"ontology/rng"
	"ontology/ver"
)

// Sentinel errors. ErrNoSolution is wrapped by *ConflictError; budget
// exhaustion and unsatisfiability are always distinguishable.
var (
	ErrNoSolution      = errors.New("solve: no solution")
	ErrBudgetExceeded  = errors.New("solve: search budget exceeded")
	ErrUnknownPackage  = errors.New("solve: unknown package")
	ErrInvalidSolution = errors.New("solve: invalid solution")
)

// Solver solves requirement sets against a graph. It is safe for
// concurrent use; the only shared mutable state is the attempts counter.
type Solver struct {
	g           *graph.Graph
	maxAttempts int64
	attempts    atomic.Int64 // unexported counter, not part of Solve's API
}

// Option configures a Solver.
type Option func(*Solver)

// WithMaxAttempts caps the number of package@version combinations tried
// per Solve call; exceeding it returns ErrBudgetExceeded.
func WithMaxAttempts(n int64) Option {
	return func(s *Solver) { s.maxAttempts = n }
}

// New returns a Solver over g. The default attempt budget is 1<<20.
func New(g *graph.Graph, opts ...Option) *Solver {
	s := &Solver{g: g, maxAttempts: 1 << 20}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Attempts returns the total number of package@version combinations tried
// across all Solve calls on this Solver.
func (s *Solver) Attempts() int64 { return s.attempts.Load() }

// Solve finds a satisfying assignment for roots.
func (s *Solver) Solve(roots []Requirement) (Solution, error) {
	sch := &search{
		g:      s.g,
		max:    s.maxAttempts,
		assign: map[string]ver.Version{},
		imp:    map[string][]imposed{},
	}
	defer func() { s.attempts.Add(sch.tried) }()
	rs := append([]Requirement(nil), roots...)
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].Package != rs[j].Package {
			return rs[i].Package < rs[j].Package
		}
		return rs[i].Constraint < rs[j].Constraint
	})
	for _, r := range rs {
		con, err := rng.Parse(r.Constraint)
		if err != nil {
			return nil, err
		}
		if !s.g.Has(r.Package) {
			return nil, fmt.Errorf("%w: %q", ErrUnknownPackage, r.Package)
		}
		sch.imp[r.Package] = append(sch.imp[r.Package],
			imposed{raw: r.Constraint, con: con})
	}
	sol, err := sch.run()
	if errors.Is(err, errConflict) {
		return nil, &ConflictError{Chain: sch.best}
	}
	return sol, err
}
