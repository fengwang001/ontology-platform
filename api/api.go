// Package api is the public facade over the incremental INTERSECT ALL view.
package api

import (
	"errors"
	"fmt"

	"ontology/inter"
	"ontology/mset"
)

// Re-exported so callers only need this package.
type (
	Side   = mset.Side
	Change = inter.Change
)

const (
	L = mset.L
	R = mset.R
)

var (
	ErrZeroDelta     = mset.ErrZeroDelta
	ErrNegativeCount = mset.ErrNegativeCount
	ErrEmptyVal      = inter.ErrEmptyVal
)

// Engine maintains the materialized intersection view via a changelog.
type Engine struct{ t *inter.Table }

// New returns an empty Engine.
func New() *Engine { return &Engine{t: inter.New()} }

// Apply applies one operation and returns the changelog entries it caused.
func (e *Engine) Apply(s Side, val string, d int) ([]Change, error) {
	return e.t.Apply(s, val, d)
}

// View returns each live value's intersection multiplicity.
func (e *Engine) View() map[string]int { return e.t.View() }

// SelfCheck runs built-in operation sequences against a private instance
// and verifies the four invariants. It is safe for concurrent use.
func (e *Engine) SelfCheck() error {
	eng := New()
	// Invariants 1+2: replay a mixed sequence, tracking l/r per value, and
	// check every changelog prefix against batch min(l, r).
	type op struct {
		s Side
		v string
		d int
	}
	ops := []op{
		{L, "a", 1}, {R, "a", 1}, {R, "a", 1}, {L, "a", 1}, {R, "a", 1},
		{R, "a", -1}, {R, "a", -1}, {L, "a", -1},
		{L, "b", 2}, {R, "b", 1}, {L, "b", -1}, {R, "b", 1},
		{L, mset.Null, 1}, {R, mset.Null, 1}, {L, "c", 3}, {L, "c", -3},
	}
	l, r := map[string]int{}, map[string]int{}
	m := map[string]int{}
	for i, o := range ops {
		cs, err := eng.Apply(o.s, o.v, o.d)
		if err != nil {
			return fmt.Errorf("selfcheck op %d: %w", i, err)
		}
		if o.s == L {
			l[o.v] += o.d
		} else {
			r[o.v] += o.d
		}
		for _, c := range cs {
			if c.Delta != 1 && c.Delta != -1 {
				return fmt.Errorf("selfcheck: non-unit delta %d", c.Delta)
			}
			if c.Delta == -1 && m[c.Val] <= 0 {
				return errors.New("selfcheck: retract of absent copy")
			}
			m[c.Val] += c.Delta
			if m[c.Val] < 0 {
				return errors.New("selfcheck: negative multiplicity")
			}
		}
		for v := range m {
			if m[v] != want(l, r, v) {
				return fmt.Errorf("selfcheck: prefix %d value %q: m=%d want %d", i, v, m[v], want(l, r, v))
			}
		}
	}
	// Invariant 1 (final state) and NULL never matching.
	for v, got := range eng.View() {
		if got != want(l, r, v) {
			return fmt.Errorf("selfcheck: view %q=%d want %d", v, got, want(l, r, v))
		}
	}
	// Invariant 4: rejected ops leave no trace and errors are distinct.
	before := eng.View()
	for _, o := range []op{{L, "a", 0}, {R, "a", -99}, {L, "", 1}} {
		if _, err := eng.Apply(o.s, o.v, o.d); err == nil {
			return fmt.Errorf("selfcheck: op %+v unexpectedly accepted", o)
		}
	}
	if !(ErrZeroDelta != ErrNegativeCount && ErrNegativeCount != ErrEmptyVal && ErrZeroDelta != ErrEmptyVal) {
		return errors.New("selfcheck: sentinel errors not distinct")
	}
	for v, got := range eng.View() {
		if before[v] != got {
			return fmt.Errorf("selfcheck: rejected op changed view at %q", v)
		}
	}
	return nil
}

func want(l, r map[string]int, v string) int {
	if v == mset.Null {
		return 0
	}
	if l[v] < r[v] {
		return l[v]
	}
	return r[v]
}
