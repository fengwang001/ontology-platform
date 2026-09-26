// Package api is the external facade over a replica: api -> repl -> sm.
package api

import (
	"errors"
	"fmt"

	"ontology/repl"
	"ontology/sm"
)

var (
	ErrCommitOutOfRange   = repl.ErrCommitOutOfRange
	ErrEmptyCommand       = repl.ErrEmptyCommand
	ErrSnapshotOutOfRange = repl.ErrSnapshotOutOfRange
)

type API struct{ r *repl.Replica }

func New() *API { return &API{r: repl.New()} }
func newWith(cs ...sm.Cmd) *API {
	a := New()
	for _, c := range cs {
		_ = a.Append(c)
	}
	return a
}
func (a *API) Append(c sm.Cmd) error  { return a.r.Append(c) }
func (a *API) Commit(i int) error     { return a.r.Commit(i) }
func (a *API) Apply()                 { a.r.Apply() }
func (a *API) Restart(i, s int) error { return a.r.Restart(i, s) }
func (a *API) State() int             { return a.r.State() }
func (a *API) LastApplied() int       { return a.r.LastApplied() }
func (a *API) Committed() int         { return a.r.Committed() }

// LastApplyReadOne exposes a verdict only, never the counter's numeric value.
func (a *API) LastApplyReadOne() bool { return a.r.LastApplyReadOne() }

func fold(b int, cs []sm.Cmd) int {
	for _, c := range cs {
		b = sm.Apply(c, b)
	}
	return b
}

// SelfCheck verifies the four invariants and incremental reading on fresh replicas.
func (a *API) SelfCheck() error {
	four := []sm.Cmd{sm.Add(2), sm.Mul(3), sm.Add(1), sm.Add(5)}
	x := newWith(four...)
	g := func() [3]int { return [3]int{x.Committed(), x.LastApplied(), x.State()} }
	no := func() error { return nil }
	ap := func() error { x.Apply(); return nil }
	s4 := func() error { _ = x.Restart(2, 6); _ = x.Commit(4); return nil }
	steps := []struct {
		do func() error
		w  [3]int
	}{
		{no, [3]int{0, 0, 0}},
		{func() error { return x.Commit(3) }, [3]int{3, 0, 0}},
		{ap, [3]int{3, 3, 7}},
		{s4, [3]int{4, 2, 6}},
		{ap, [3]int{4, 4, 12}},
	}
	for i, s := range steps {
		if e := s.do(); e != nil {
			return e
		}
		if g() != s.w {
			return fmt.Errorf("SelfCheck S%d: got %v want %v", i+1, g(), s.w)
		}
	}
	if fold(0, four[:3]) != 7 || fold(6, four[2:4]) != 12 { // invariant 1
		return errors.New("SelfCheck: naive recompute mismatch")
	}
	if e := checkConvergence(); e != nil {
		return e
	}
	if e := checkRejections(); e != nil {
		return e
	}
	for _, m := range []int{1000, 10000} {
		if !incrementalOnce(m) {
			return fmt.Errorf("SelfCheck: Apply read >1 entry at m=%d", m)
		}
	}
	return nil
}
func checkConvergence() error {
	one, many := New(), New()
	for k := 0; k < 50; k++ {
		c := sm.Mul(2)
		if k%2 == 0 {
			c = sm.Add(k + 1)
		}
		_ = one.Append(c)
		_ = many.Append(c)
	}
	_ = one.Commit(50)
	one.Apply()
	for i := 1; i <= 50; i++ {
		_ = many.Commit(i)
		many.Apply()
	}
	if one.State() != many.State() || one.LastApplied() != many.LastApplied() {
		return fmt.Errorf("SelfCheck: batching diverged %d/%d vs %d/%d",
			one.State(), one.LastApplied(), many.State(), many.LastApplied())
	}
	return nil
}
func checkRejections() error {
	a := newWith(sm.Add(5))
	_ = a.Commit(1)
	a.Apply()
	cases := []struct {
		w error
		f func() error
	}{
		{ErrEmptyCommand, func() error { return a.Append(sm.Cmd{}) }},
		{ErrCommitOutOfRange, func() error { return a.Commit(9) }},
		{ErrSnapshotOutOfRange, func() error { return a.Restart(9, 0) }},
	}
	snap := func() [3]int { return [3]int{a.Committed(), a.LastApplied(), a.State()} }
	for i, tc := range cases {
		before := snap()
		if e := tc.f(); !errors.Is(e, tc.w) {
			return fmt.Errorf("SelfCheck case %d: got %v want %v", i, e, tc.w)
		}
		if snap() != before {
			return errors.New("SelfCheck: rejected op mutated state")
		}
	}
	_ = a.Append(sm.Mul(2))
	_ = a.Commit(2)
	a.Apply()
	if a.State() != 10 {
		return errors.New("SelfCheck: replica unusable after rejected ops")
	}
	return nil
}
func incrementalOnce(m int) bool {
	a := New()
	for range m {
		_ = a.Append(sm.Add(1))
	}
	_ = a.Commit(m - 1)
	a.Apply()
	_ = a.Commit(m)
	a.Apply()
	return a.LastApplyReadOne()
}
