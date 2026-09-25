// Package api is the outward-facing surface: schema construction,
// projection setup, change-row application, and view access.
package api

import (
	"errors"
	"fmt"

	"ontology/col"
	"ontology/prune"
)

// Output is one projected column: downstream name + source refs.
type Output = prune.Output

// Store is a projection-pushdown materialized view over a wide table.
type Store struct {
	eng *prune.Engine
}

// New defines the source-column schema (ordered, non-empty, unique).
func New(cols []string) (*Store, error) {
	s, err := col.NewSchema(cols)
	if err != nil {
		return nil, err
	}
	return &Store{eng: prune.NewEngine(s)}, nil
}

// SetProjection installs the projection; invalid projections fail
// without changing any state.
func (s *Store) SetProjection(outs []Output) error {
	return s.eng.SetProjection(outs)
}

// Apply feeds one full change row; rejected rows leave no trace.
func (s *Store) Apply(row map[string]int) error {
	return s.eng.Apply(row)
}

// View returns the projected materialized view, one row per change row.
func (s *Store) View() [][]int { return s.eng.View() }

// OutputNames returns output column names in projection order.
func (s *Store) OutputNames() []string { return s.eng.OutputNames() }

// SelfCheck verifies the four invariants on a built-in projection and
// row sequence, using only fresh internal state (safe to call anytime).
func (s *Store) SelfCheck() error {
	st, err := New([]string{"a", "b", "c", "d", "e"})
	if err != nil {
		return err
	}
	outs := []Output{
		{Out: "x", Refs: []string{"a"}},
		{Out: "sum", Refs: []string{"b", "c"}},
		{Out: "y", Refs: []string{"d"}},
	}
	if err := st.SetProjection(outs); err != nil {
		return err
	}
	rows := []map[string]int{
		{"a": 1, "b": 2, "c": 3, "d": 4, "e": 5},
		{"a": 6, "b": 7, "c": 8, "d": 9, "e": 10},
		{"a": 11, "b": 12, "c": 13, "d": 14, "e": 15},
	}
	for _, r := range rows {
		if err := st.Apply(r); err != nil {
			return err
		}
	}
	// 1. consistent with full recompute; 3. stable order/names
	names := st.OutputNames()
	if fmt.Sprint(names) != "[x sum y]" {
		return fmt.Errorf("selfcheck: output names %v", names)
	}
	view := st.View()
	for i, r := range rows {
		want := []int{r["a"], r["b"] + r["c"], r["d"]}
		for j := range want {
			if view[i][j] != want[j] {
				return fmt.Errorf("selfcheck: row %d col %d got %d want %d", i, j, view[i][j], want[j])
			}
		}
	}
	// 2. pruning exact: kept = {a,b,c,d}, e pruned from every output
	if fmt.Sprint(st.eng.KeptCols()) != "[a b c d]" {
		return fmt.Errorf("selfcheck: kept %v", st.eng.KeptCols())
	}
	for _, vr := range view {
		if len(vr) != len(outs) {
			return fmt.Errorf("selfcheck: output width %d", len(vr))
		}
	}
	// 4. rejected ops leave no trace, errors are distinct
	before := fmt.Sprint(view)
	errs := []error{
		st.SetProjection([]Output{{Out: "z", Refs: []string{"nope"}}}),
		st.SetProjection([]Output{{Out: "x", Refs: []string{"a"}}, {Out: "x", Refs: []string{"b"}}}),
		st.Apply(map[string]int{"a": 1}),
		st.Apply(map[string]int{"a": 1, "b": 2, "c": 3, "d": 4, "e": 5, "zz": 6}),
	}
	for i, e := range errs {
		if e == nil {
			return fmt.Errorf("selfcheck: bad op %d accepted", i)
		}
		for j := i + 1; j < len(errs); j++ {
			if errors.Is(errs[i], errs[j]) {
				return fmt.Errorf("selfcheck: errors %d,%d not distinct", i, j)
			}
		}
	}
	if fmt.Sprint(st.View()) != before || fmt.Sprint(st.OutputNames()) != "[x sum y]" {
		return errors.New("selfcheck: rejected op changed state")
	}
	return nil
}
