// Package api is the public entry point to the INTERSECT ALL incremental
// view. It depends only on inter (and transitively mset); the dependency
// direction never points back.
package api

import (
	"errors"
	"fmt"

	"ontology/inter"
	"ontology/mset"
)

// Side re-exports the two input relations.
type Side = mset.Side

const (
	L = mset.L
	R = mset.R
)

// Distinct sentinel errors, decidable with errors.Is.
var (
	ErrZeroDelta     = mset.ErrZeroDelta
	ErrCountNegative = mset.ErrCountNegative
	ErrEmptyVal      = inter.ErrEmptyVal
)

// Change is one entry of the emitted changelog: a single +1 or -1 crossing.
type Change struct {
	Val string
	Add bool // true for +(val), false for -(val)
}

func (c Change) String() string {
	if c.Add {
		return "+(" + c.Val + ")"
	}
	return "-(" + c.Val + ")"
}

// API is the in-process materialized view.
type API struct {
	v *inter.View
}

// New returns an empty view.
func New() *API { return &API{v: inter.New()} }

// Apply adds d to side's count of val. Every unit crossing of the
// intersection multiplicity yields one Change; the whole batch is emitted
// atomically — a rejected call returns nil and changes no state.
func (a *API) Apply(side Side, val string, d int) ([]Change, error) {
	delta, err := a.v.Apply(side, val, d)
	if err != nil {
		return nil, err
	}
	ch := make([]Change, 0, abs(delta))
	for i := 0; i < abs(delta); i++ {
		ch = append(ch, Change{Val: val, Add: delta > 0})
	}
	return ch, nil
}

// View returns the current intersection multiplicities.
func (a *API) View() map[string]int { return a.v.View() }

// SelfCheck replays built-in sequences and verifies the four invariants.
// It returns nil when all hold, otherwise an error describing the failure.
func (a *API) SelfCheck() error {
	fresh := New()
	// Eight-step canonical sequence; track raw counts and a downstream view.
	type op struct {
		side Side
		val  string
		d    int
		want int // expected signed number of changelog entries
	}
	ops := []op{
		{L, "a", 1, 0}, {R, "a", 1, 1}, {R, "a", 1, 0}, {L, "a", 1, 1},
		{R, "a", 1, 0}, {R, "a", -1, 0}, {R, "a", -1, -1}, {L, "a", -1, 0},
	}
	l, r := 0, 0
	down := map[string]int{}
	for i, o := range ops {
		if o.side == L {
			l += o.d
		} else {
			r += o.d
		}
		ch, err := fresh.Apply(o.side, o.val, o.d)
		if err != nil {
			return fmt.Errorf("step %d: unexpected error: %w", i+1, err)
		}
		if len(ch) != abs(o.want) || (len(ch) > 0 && ch[0].Add != (o.want > 0)) {
			return fmt.Errorf("step %d: changelog %v, want sign %d", i+1, ch, o.want)
		}
		for _, c := range ch { // downstream prefix application
			if !c.Add && down[c.Val] == 0 {
				return fmt.Errorf("step %d: minus with no live copy", i+1)
			}
			if c.Add {
				down[c.Val]++
			} else {
				down[c.Val]--
			}
		}
		m := min(l, r)
		if down["a"] != m || fresh.View()["a"] != m {
			return fmt.Errorf("step %d: view diverges from min(%d,%d)=%d", i+1, l, r, m)
		}
	}
	// NULL never matches, even on both sides.
	if _, err := fresh.Apply(L, mset.NullMarker, 1); err != nil {
		return err
	}
	if _, err := fresh.Apply(R, mset.NullMarker, 1); err != nil {
		return err
	}
	if _, ok := fresh.View()[mset.NullMarker]; ok {
		return errors.New("NULL must not appear in the view")
	}
	// Rejected operations are atomic and leave no trace.
	before := len(fresh.View())
	for _, bad := range []struct {
		side Side
		val  string
		d    int
		want error
	}{
		{L, "z", 0, ErrZeroDelta},
		{L, "z", -1, ErrCountNegative},
		{L, "", 1, ErrEmptyVal},
	} {
		if _, err := fresh.Apply(bad.side, bad.val, bad.d); !errors.Is(err, bad.want) {
			return fmt.Errorf("want %v, got %v", bad.want, err)
		}
	}
	if len(fresh.View()) != before {
		return errors.New("rejected operation changed state")
	}
	return nil
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
