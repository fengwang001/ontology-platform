// Package api exposes the fixed materialized-view exercise to callers.
package api

import (
	"errors"
	"fmt"
	"maps"

	"ontology/dep"
	"ontology/mview"
)

// The four failures are pairwise-distinct decidable sentinel errors.
var (
	ErrNotFound     = mview.ErrNotFound
	ErrUpdateView   = mview.ErrUpdateView
	ErrRefreshBase  = mview.ErrRefreshBase
	ErrDepsNotReady = mview.ErrDepsNotReady

	initial  = map[string]int{"B1": 10, "B2": 20, "B3": 30, "V1": 30, "V2": 50, "V3": 80}
	viewDeps = map[string][]string{"V1": {"B1", "B2"}, "V2": {"B2", "B3"}, "V3": {"V1", "V2"}}
	trans    = map[string][]string{"V1": {"B1", "B2"}, "V2": {"B2", "B3"}, "V3": {"B1", "B2", "B3"}}
)

// Engine is the ready-to-use fixed graph (B1..B3 bases, V1..V3 views).
type Engine struct{ s *mview.State }

// New builds the fixed graph (epoch 0, all revisions 0, all views valid).
func New() *Engine {
	g := dep.New(
		dep.Node{Name: "B1", Kind: dep.Base}, dep.Node{Name: "B2", Kind: dep.Base}, dep.Node{Name: "B3", Kind: dep.Base},
		dep.Node{Name: "V1", Kind: dep.View, Deps: []string{"B1", "B2"}},
		dep.Node{Name: "V2", Kind: dep.View, Deps: []string{"B2", "B3"}},
		dep.Node{Name: "V3", Kind: dep.View, Deps: []string{"V1", "V2"}},
	)
	add := func(v []int) int { return v[0] + v[1] }
	exprs := map[string]mview.Expr{"V1": add, "V2": add, "V3": add}
	return &Engine{s: mview.New(g, initial, exprs)}
}

func (e *Engine) UpdateBase(name string, val int) error { return e.s.UpdateBase(name, val) }
func (e *Engine) Refresh(name string) error             { return e.s.Refresh(name) }
func (e *Engine) Value(name string) (int, error)        { return e.s.Value(name) }
func (e *Engine) IsStale(name string) bool              { st, err := e.s.IsStale(name); return err == nil && st }

// refModel is an independent implementation of the rules, used by SelfCheck.
type refModel struct {
	val, rev map[string]int
	epoch    int
}

func newRef() *refModel { return &refModel{val: maps.Clone(initial), rev: map[string]int{}} }

func (r *refModel) stale(v string) bool {
	for _, b := range trans[v] {
		if r.rev[b] > r.rev[v] {
			return true
		}
	}
	return false
}

func (r *refModel) update(b string, val int) { r.epoch++; r.val[b], r.rev[b] = val, r.epoch }

func (r *refModel) refresh(v string) bool {
	for _, d := range viewDeps[v] {
		if d[0] == 'V' && r.stale(d) {
			return false
		}
	}
	sum := 0
	for _, d := range viewDeps[v] {
		sum += r.val[d]
	}
	r.val[v], r.rev[v] = sum, r.epoch
	return true
}

// naive recomputes a view from scratch from current base values (invariant 1).
func (r *refModel) naive(v string) int {
	v1, v2 := r.val["B1"]+r.val["B2"], r.val["B2"]+r.val["B3"]
	return map[string]int{"V1": v1, "V2": v2, "V3": v1 + v2}[v]
}

type scOp struct {
	k   byte
	n   string
	val int
}

// SelfCheck replays built-in sequences, verifying the four invariants and the four distinct errors with no trace from rejects.
func (e *Engine) SelfCheck() error {
	en, rf := New(), newRef()
	ops := []scOp{
		{'u', "B2", 25}, {'r', "V3", 0}, {'r', "V1", 0}, {'r', "V2", 0}, {'r', "V3", 0},
		{'u', "B1", 100}, {'r', "V1", 0}, {'r', "V3", 0},
		{'u', "B3", 7}, {'r', "V2", 0}, {'r', "V3", 0},
	}
	for i, o := range ops {
		if o.k == 'u' {
			if err := en.UpdateBase(o.n, o.val); err != nil {
				return fmt.Errorf("step %d: %w", i, err)
			}
			rf.update(o.n, o.val)
		} else if ok := rf.refresh(o.n); ok != (en.Refresh(o.n) == nil) {
			return fmt.Errorf("step %d refresh %s acceptance mismatch", i, o.n)
		}
		for _, v := range []string{"V1", "V2", "V3"} {
			gv, _ := en.Value(v)
			gs := en.IsStale(v)
			if gv != rf.val[v] || gs != rf.stale(v) {
				return fmt.Errorf("step %d %s: engine(%d,%v) ref(%d,%v)", i, v, gv, gs, rf.val[v], rf.stale(v))
			}
			if !gs && gv != rf.naive(v) {
				return fmt.Errorf("step %d %s violates naive recomputation", i, v)
			}
		}
	}
	return checkErrors()
}

func checkErrors() error {
	en := New()
	cases := []struct {
		want error
		fn   func() error
	}{
		{ErrNotFound, func() error { return en.UpdateBase("Nope", 1) }},
		{ErrUpdateView, func() error { return en.UpdateBase("V1", 1) }},
		{ErrRefreshBase, func() error { return en.Refresh("B1") }},
		{ErrNotFound, func() error { return en.Refresh("Nope") }},
	}
	for i, c := range cases {
		if !errors.Is(c.fn(), c.want) {
			return fmt.Errorf("error case %d", i)
		}
	}
	if err := en.UpdateBase("B2", 25); err != nil {
		return err
	}
	if err := en.Refresh("V3"); !errors.Is(err, ErrDepsNotReady) {
		return fmt.Errorf("deps-not-ready got %v", err)
	}
	if v, _ := en.Value("V3"); v != 80 {
		return fmt.Errorf("rejected Refresh mutated V3 to %d", v)
	}
	return en.Refresh("V1")
}
