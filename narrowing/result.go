package narrowing

import "sync/atomic"

// Point selects which side of a statement a query refers to.
type Point int

const (
	// Before is the program point immediately before a statement runs.
	Before Point = iota
	// After is the program point immediately after a statement (for a
	// conditional: after both branches have joined).
	After
)

// Result is the immutable outcome of a successful analysis. It is safe
// for concurrent queries by any number of callers, and independent
// analyses may run concurrently: results are equivalent to some serial
// execution order because no shared mutable state exists.
//
// Every query is answered by a constant number of hash-map lookups over
// precomputed snapshots; the cost depends only on the size of the
// queried variable's narrowed type, never on the number of statements,
// and never triggers re-analysis. The Steps counter makes this
// verifiable: each query advances it by a small constant.
type Result struct {
	declared map[string]Type
	pre      map[StatementID]map[string]Type
	post     map[StatementID]map[string]Type
	steps    atomic.Uint64
}

// Query returns the narrowed type of a variable at a program point.
//
// ok is false when the statement identifier or the variable name is
// unknown. reachable is false when the point is unreachable on every
// path (typ is the never type then); this is deliberately distinct from
// a present narrowing result.
func (r *Result) Query(id StatementID, p Point, name string) (typ Type, reachable, ok bool) {
	r.steps.Add(1)
	table := r.pre
	if p == After {
		table = r.post
	}
	r.steps.Add(1)
	env, found := table[id]
	if !found {
		return Type{}, false, false
	}
	r.steps.Add(1)
	if _, declared := r.declared[name]; !declared {
		return Type{}, false, false
	}
	if env == nil {
		return Never(), false, true
	}
	r.steps.Add(1)
	return env[name], true, true
}

// QuerySteps returns the total number of internal steps charged to
// queries so far. Deltas around a single Query call are a small
// constant independent of program size, which tests use to verify the
// query-cost guarantee.
func (r *Result) QuerySteps() uint64 { return r.steps.Load() }

// Declared returns the declared type of a variable.
func (r *Result) Declared(name string) (Type, bool) {
	r.steps.Add(1)
	t, ok := r.declared[name]
	return t, ok
}
