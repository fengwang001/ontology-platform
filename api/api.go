// Package api is the external face of the incremental COUNT-DISTINCT engine.
// It depends only on package dagg.
package api

import (
	"errors"
	"fmt"

	"ontology/dagg"
)

type (
	// Change is one ordered mutation: Sign +1 inserts once, -1 withdraws once.
	Change = dagg.Change
	// Out is one changelog entry.
	Out = dagg.Out
)

// Judgeable sentinel errors; the three failure classes are always distinct.
var (
	ErrWithdrawAbsent = dagg.ErrWithdrawAbsent
	ErrInvalidChange  = dagg.ErrInvalidChange
	ErrLimitExceeded  = dagg.ErrLimitExceeded
)

// Engine is the process-resident materialized-view engine.
type Engine struct{ d *dagg.Agg }

// New creates an Engine capped at maxEntries live (Group,Val) entries.
func New(maxEntries int) *Engine { return &Engine{d: dagg.New(maxEntries)} }

// Feed applies one batch atomically and returns its changelog entries.
func (e *Engine) Feed(batch []Change) ([]Out, error) { return e.d.Feed(batch) }

// View returns group -> distinct count (zero-count groups omitted).
func (e *Engine) View() map[string]int { return e.d.View() }

// Log returns all changelog entries emitted so far.
func (e *Engine) Log() []Out { return e.d.Log() }

// SelfCheck replays a built-in sequence on throwaway engines and verifies the
// four invariants.
func (e *Engine) SelfCheck() error {
	g := "g"
	good := [][]Change{
		{dagg.C(g, "a", 1)}, {dagg.C(g, "b", 1)}, {dagg.C(g, "a", 1)}, {dagg.C(g, "a", -1)},
		{dagg.C(g, "b", -1)}, {dagg.C(g, "c", 1), dagg.C(g, "c", -1)},
		{dagg.C(g, "a", -1)}, {dagg.C(g, "a", 1)},
		{dagg.C("h", "x", 1), dagg.C("k", "y", 1), dagg.C("h", "x", -1)}, // h nets to zero
	}
	eng := New(10)
	mult := map[[2]string]int{}
	for _, b := range good {
		out, err := eng.Feed(b)
		if err != nil {
			return fmt.Errorf("selfcheck: built-in batch rejected: %w", err)
		}
		if err := CheckBatch(out); err != nil {
			return err
		}
		for _, c := range b {
			k := [2]string{c.Group, c.Val}
			if mult[k] += c.Sign; mult[k] == 0 {
				delete(mult, k) // invariant 3: zero entries never linger
			}
			if mult[k] < 0 {
				return errors.New("selfcheck: invariant 3 negative multiplicity")
			}
		}
		if fmt.Sprint(eng.View()) != fmt.Sprint(recompute(mult)) {
			return errors.New("selfcheck: invariant 1 view != batch recompute")
		}
	}
	if err := CheckLog(eng.Log()); err != nil {
		return err
	}
	bad := []struct {
		max   int
		batch []Change
		want  error
	}{ // b was withdrawn in the good sequence, so it is now absent:
		{10, []Change{dagg.C(g, "b", -1)}, ErrWithdrawAbsent},
		{10, []Change{dagg.C(g, "z", 0)}, ErrInvalidChange},
		{10, []Change{dagg.C("", "z", 1)}, ErrInvalidChange},
		{10, []Change{dagg.C(g, "", 1)}, ErrInvalidChange},
		{1, []Change{dagg.C("p", "1", 1), dagg.C("p", "2", 1)}, ErrLimitExceeded},
	}
	for i, tc := range bad {
		p := New(tc.max)
		v0, n0 := fmt.Sprint(p.View()), len(p.Log())
		_, err := p.Feed(tc.batch)
		if !errors.Is(err, tc.want) || fmt.Sprint(p.View()) != v0 || len(p.Log()) != n0 {
			return fmt.Errorf("selfcheck case %d: wrong result or trace left (got %v)", i, err)
		}
		if _, err := p.Feed(good[0]); err != nil { // still usable after reject
			return fmt.Errorf("selfcheck case %d: engine unusable: %w", i, err)
		}
	}
	return nil
}

// CheckLog replays every prefix of the whole log: at most one held value per
// group and each '-' withdraws exactly it. The equal-valued -(G,n) +(G,n) ban
// is per-batch (a group may vanish and reappear at the same count next batch);
// check each Feed slice with CheckBatch.
func CheckLog(log []Out) error {
	held := map[string]int{}
	for i, e := range log {
		switch e.Sign {
		case -1:
			if cur, ok := held[e.Group]; !ok || cur != e.N {
				return fmt.Errorf("invariant 2: bad '-' at entry %d", i)
			}
			delete(held, e.Group)
		case 1:
			if _, ok := held[e.Group]; ok {
				return fmt.Errorf("invariant 2: two held values at entry %d", i)
			}
			held[e.Group] = e.N
		default:
			return errors.New("invariant 2: changelog sign out of range")
		}
	}
	return nil
}

// CheckBatch verifies a same-group adjacent -(G,o) +(G,n) pair never o == n.
func CheckBatch(out []Out) error {
	for i := 1; i < len(out); i++ {
		p, c := out[i-1], out[i]
		if p.Sign == -1 && c.Sign == 1 && p.Group == c.Group && p.N == c.N {
			return fmt.Errorf("invariant 2: equal -(G,n) +(G,n) pair at %d", i)
		}
	}
	return nil
}

func recompute(mult map[[2]string]int) map[string]int {
	v := map[string]int{}
	for k, n := range mult {
		if n > 0 {
			v[k[0]]++
		}
	}
	return v
}
