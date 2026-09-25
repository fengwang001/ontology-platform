// Package api is the public entry point: apply whole-row events, read the
// materialized view, recompute, and self-check. It depends only on package row.
package api

import (
	"errors"
	"fmt"
	"reflect"

	"ontology/diff"
	"ontology/row"
)

// Sentinel errors for the three rejected, distinguishable inputs.
var (
	ErrNilEvent    = errors.New("api: after event is nil (not a clear; use an empty non-nil map)")
	ErrEmptyKey    = errors.New("api: key must not be the empty string")
	ErrEmptyColumn = errors.New("api: column name must not be the empty string")
)

// Re-export the change vocabulary so callers need one import.
type (
	Change = diff.Change
	Kind   = diff.Kind
)

const (
	Added   = diff.Added
	Removed = diff.Removed
	Changed = diff.Changed
)

// API is the in-memory column-level diff service.
type API struct {
	store *row.Store
}

// New returns an empty API.
func New() *API { return &API{store: row.New()} }

// Apply validates the event and, if valid, replaces key's row by after and
// returns the column-level changes. Every check runs before any mutation, so
// a rejected event leaves both current rows and the changelog untouched.
func (a *API) Apply(key string, after map[string]string) ([]Change, error) {
	if key == "" {
		return nil, ErrEmptyKey
	}
	if after == nil {
		return nil, ErrNilEvent
	}
	for col := range after {
		if col == "" {
			return nil, ErrEmptyColumn
		}
	}
	return a.store.Apply(key, after), nil
}

// View returns a deep copy of the current row of every key.
func (a *API) View() map[string]map[string]string { return a.store.View() }

// Recompute returns the one-shot full diff from the empty row to the current
// row of key.
func (a *API) Recompute(key string) []Change { return a.store.Recompute(key) }

// replay applies a changelog to a starting row and returns the result.
func replay(start map[string]string, changes []Change) map[string]string {
	out := clone(start)
	for _, c := range changes {
		switch c.Kind {
		case Added, Changed:
			out[c.Col] = c.New
		case Removed:
			delete(out, c.Col)
		}
	}
	return out
}

func clone(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// SelfCheck runs the built-in six-event sequence of the spec and verifies
// the four invariants, including rejected events leaving no trace.
func (a *API) SelfCheck() error {
	const key = "k"
	events := []map[string]string{
		{"a": "1", "b": "x"},
		{"a": "1", "b": "y", "c": ""},
		{"a": "2", "b": "y", "c": ""},
		{"a": "2", "c": ""},
		{"c": "z"},
		{},
	}
	want := [][]Change{
		{{Kind: Added, Col: "a", New: "1"}, {Kind: Added, Col: "b", New: "x"}},
		{{Kind: Changed, Col: "b", Old: "x", New: "y"}, {Kind: Added, Col: "c"}},
		{{Kind: Changed, Col: "a", Old: "1", New: "2"}},
		{{Kind: Removed, Col: "b", Old: "y"}},
		{{Kind: Removed, Col: "a", Old: "2"}, {Kind: Changed, Col: "c", Old: "", New: "z"}},
		{{Kind: Removed, Col: "c", Old: "z"}},
	}
	var all []Change
	for i, ev := range events {
		got, err := a.Apply(key, ev)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(got, want[i]) {
			return fmt.Errorf("step %d: got %v want %v", i+1, got, want[i])
		}
		all = append(all, got...)
		// Invariant 1: full recompute and incremental replay both rebuild
		// the current row column-for-column.
		cur := a.View()[key]
		if !reflect.DeepEqual(replay(nil, all), cur) {
			return fmt.Errorf("step %d: replay diverges from view", i+1)
		}
		if !reflect.DeepEqual(replay(nil, a.Recompute(key)), cur) {
			return fmt.Errorf("step %d: recompute diverges from view", i+1)
		}
	}
	// Invariant 4: the three rejections are distinct errors and leave no trace.
	before := a.View()
	if _, err := a.Apply("", map[string]string{"a": "1"}); !errors.Is(err, ErrEmptyKey) {
		return fmt.Errorf("empty key: got %v", err)
	}
	if _, err := a.Apply("x", nil); !errors.Is(err, ErrNilEvent) {
		return fmt.Errorf("nil event: got %v", err)
	}
	if _, err := a.Apply("x", map[string]string{"": "v"}); !errors.Is(err, ErrEmptyColumn) {
		return fmt.Errorf("empty column: got %v", err)
	}
	if !reflect.DeepEqual(a.View(), before) {
		return errors.New("rejected events mutated state")
	}
	if _, err := a.Apply("x", map[string]string{"a": "1"}); err != nil {
		return fmt.Errorf("service unusable after rejections: %w", err)
	}
	return nil
}
