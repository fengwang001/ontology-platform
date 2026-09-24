// Package api is the public façade over gagg.
package api

import (
	"errors"
	"fmt"

	"ontology/gagg"
	"ontology/gdelta"
)

// Public types are aliases so callers and the changelog share one shape.
type (
	Op     = gdelta.Op
	Change = gdelta.Change
	Agg    = gdelta.Agg
)

// Op constructors.
var (
	Insert = gdelta.Insert
	Update = gdelta.Update
	Delete = gdelta.Delete
)

// Sentinel errors, re-exported for errors.Is.
var (
	ErrRowExists   = gagg.ErrRowExists
	ErrRowNotFound = gagg.ErrRowNotFound
	ErrEmptyGroup  = gagg.ErrEmptyGroup
	ErrTooMany     = gagg.ErrTooMany
)

// Engine is the materialized-view engine.
type Engine struct {
	t *gagg.Table
}

// New creates an engine bounded by maxGroups groups.
func New(maxGroups int) *Engine { return &Engine{t: gagg.New(maxGroups)} }

// Apply applies one batch atomically and returns its changelog entries.
func (e *Engine) Apply(ops []Op) ([]Change, error) { return e.t.Apply(ops) }

// View returns the materialized view: group -> (sum, count), count > 0 only.
func (e *Engine) View() map[string]Agg { return e.t.View() }

// SelfCheck verifies the four invariants on a built-in operation sequence.
func (e *Engine) SelfCheck() error {
	eng := New(4)                   // built-in sequence needs groups a..d; run on a fresh engine
	rows := map[int64]gdelta.Row{}  // independent reference row table
	down := map[string]gdelta.Agg{} // downstream replay of every prefix
	steps := []gdelta.Op{
		Insert(1, "a", 5), Insert(2, "a", -5), Insert(3, "b", 7),
		Update(1, "a", 8), Update(2, "b", 3), Update(1, "c", 8),
		Update(3, "b", 7), Delete(2), Insert(2, "b", 0),
	}
	mirror := func(op gdelta.Op) {
		switch op.Kind {
		case gdelta.InsertOp:
			rows[op.ID] = gdelta.Row{G: op.G, V: op.V}
		case gdelta.UpdateOp:
			rows[op.ID] = gdelta.Row{G: op.G, V: op.V}
		case gdelta.DeleteOp:
			delete(rows, op.ID)
		}
	}
	replay := func(cs []Change) error { // I2: every prefix is self-consistent
		perGroup := map[string]int{}
		for _, c := range cs {
			perGroup[c.G]++
			if !c.Add {
				cur, ok := down[c.G]
				if !ok || cur != (Agg{Sum: c.Sum, Count: c.Count}) {
					return fmt.Errorf("retract mismatch: %+v cur=%v ok=%v", c, cur, ok)
				}
				delete(down, c.G)
			} else {
				if _, ok := down[c.G]; ok {
					return fmt.Errorf("group %s already present on +", c.G)
				}
				down[c.G] = Agg{Sum: c.Sum, Count: c.Count}
			}
			if c.Count <= 0 {
				return fmt.Errorf("entry with count<=0: %+v", c)
			}
		}
		for g, n := range perGroup { // I3: at most one - and one + per group
			if n > 2 {
				return fmt.Errorf("group %s emitted %d entries in one op", g, n)
			}
		}
		if len(cs) > 4 { // I3: at most four entries per op
			return fmt.Errorf("op emitted %d entries", len(cs))
		}
		return nil
	}

	for _, op := range steps {
		cs, err := eng.Apply([]Op{op})
		if err != nil {
			return fmt.Errorf("legal op rejected: %w", err)
		}
		if err := replay(cs); err != nil {
			return err
		}
		mirror(op)
	}
	// I4: a rejected batch (empty key mid-batch) changes nothing and emits
	// nothing, and the engine stays usable afterwards.
	before := eng.View()
	if cs, err := eng.Apply([]Op{Insert(9, "d", 1), Insert(10, "", 1)}); !errors.Is(err, ErrEmptyGroup) || cs != nil {
		return fmt.Errorf("rejected batch: err=%v cs=%v", err, cs)
	}
	if !mapsEqual(before, eng.View()) {
		return errors.New("rejected batch left a trace")
	}

	want := map[string]Agg{} // I1: naive recompute over the reference rows
	for _, r := range rows {
		a := want[r.G]
		want[r.G] = Agg{Sum: a.Sum + r.V, Count: a.Count + 1}
	}
	if !mapsEqual(want, eng.View()) {
		return fmt.Errorf("I1 view mismatch: want=%v got=%v", want, eng.View())
	}
	if !mapsEqual(down, eng.View()) { // I2: full replay equals View
		return fmt.Errorf("I2 replay mismatch: %v vs %v", down, eng.View())
	}
	for _, a := range eng.View() { // I3: no count==0 group ever visible
		if a.Count <= 0 {
			return errors.New("view contains count<=0 group")
		}
	}
	return nil
}

func mapsEqual(a, b map[string]Agg) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
