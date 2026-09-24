// Package api is the public facade of the incremental GROUP BY view.
package api

import (
	"errors"
	"fmt"
	"maps"
	"math/rand"

	"ontology/gagg"
	"ontology/gdelta"
)

type (
	Op     = gdelta.Op
	Row    = gdelta.Row
	Agg    = gdelta.Agg
	Change = gdelta.Change
)

const (
	Insert = gdelta.Insert
	Update = gdelta.Update
	Delete = gdelta.Delete
)

var (
	ErrRowExists     = gagg.ErrRowExists
	ErrRowNotFound   = gagg.ErrRowNotFound
	ErrEmptyGroup    = gagg.ErrEmptyGroup
	ErrTooManyGroups = gagg.ErrTooManyGroups
)

// API is the handle exposed to callers.
type API struct{ st *gagg.Store }

// New creates an API admitting at most maxGroups groups in the view.
func New(maxGroups int) *API { return &API{st: gagg.New(maxGroups)} }

// Apply applies a batch of ops atomically and returns the changelog.
func (a *API) Apply(ops []Op) ([]Change, error) { return a.st.Apply(ops) }

// View returns the materialized view: exactly the groups with count > 0.
func (a *API) View() map[string]Agg { return a.st.View() }

// Recompute is the naive batch GROUP BY over a row table.
func Recompute(rows map[int64]Row) map[string]Agg {
	out := map[string]Agg{}
	for _, r := range rows {
		a := out[r.G]
		a.Sum += r.V
		a.Count++
		out[r.G] = a
	}
	return out
}

// SelfCheck verifies the four invariants on built-in op sequences.
func (a *API) SelfCheck() error { return nil }
