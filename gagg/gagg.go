// Package gagg maintains the row table and per-group aggregates and
// applies batches of operations atomically.
package gagg

import (
	"errors"
	"sync"

	"ontology/gdelta"
)

var (
	ErrRowExists     = errors.New("gagg: row already exists")
	ErrRowNotFound   = errors.New("gagg: row not found")
	ErrEmptyGroup    = errors.New("gagg: empty group key")
	ErrTooManyGroups = errors.New("gagg: too many groups")
)

// Store holds the row table and the per-group aggregates.
type Store struct {
	mu        sync.RWMutex
	rows      map[int64]gdelta.Row
	aggs      map[string]gdelta.Agg
	maxGroups int
	reads     int // rows read by the most recent op (unexported on purpose)
}

// New creates an empty Store admitting at most maxGroups groups.
func New(maxGroups int) *Store {
	return &Store{
		rows:      make(map[int64]gdelta.Row),
		aggs:      make(map[string]gdelta.Agg),
		maxGroups: maxGroups,
	}
}

// Apply applies a batch of ops atomically and returns the changelog.
// It works on clones of the row table and aggregates; any rejected op
// discards the clones, so a failed batch leaves no trace.
func (s *Store) Apply(ops []gdelta.Op) ([]gdelta.Change, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := make(map[int64]gdelta.Row, len(s.rows))
	for id, r := range s.rows {
		rows[id] = r
	}
	aggs := make(map[string]gdelta.Agg, len(s.aggs))
	for g, a := range s.aggs {
		aggs[g] = a
	}
	var out []gdelta.Change
	for _, op := range ops {
		s.reads = 0
		cs, err := applyOne(rows, aggs, s.maxGroups, op, &s.reads)
		if err != nil {
			return nil, err
		}
		out = append(out, cs...)
	}
	s.rows, s.aggs = rows, aggs
	return out, nil
}

// applyOne validates and applies a single op against the given maps.
func applyOne(rows map[int64]gdelta.Row, aggs map[string]gdelta.Agg, maxGroups int, op gdelta.Op, reads *int) ([]gdelta.Change, error) {
	if op.Kind != gdelta.Delete && op.G == "" {
		return nil, ErrEmptyGroup
	}
	*reads++
	old, ok := rows[op.ID]
	switch op.Kind {
	case gdelta.Insert:
		if ok {
			return nil, ErrRowExists
		}
	case gdelta.Update, gdelta.Delete:
		if !ok {
			return nil, ErrRowNotFound
		}
	}
	var oldp, newp *gdelta.Row
	var oldKey, newKey string
	if ok {
		r := old
		oldp = &r
		oldKey = old.G
	}
	if op.Kind != gdelta.Delete {
		n := gdelta.Row{G: op.G, V: op.V}
		newp = &n
		newKey = op.G
	}
	cs := gdelta.Entries(oldp, newp, aggs[oldKey], aggs[newKey])
	add := func(g string, dv, dc int64) {
		a := aggs[g]
		a.Sum += dv
		a.Count += dc
		if a.Count == 0 {
			delete(aggs, g)
		} else {
			aggs[g] = a
		}
	}
	if ok {
		add(old.G, -old.V, -1)
	}
	if op.Kind == gdelta.Delete {
		delete(rows, op.ID)
	} else {
		add(op.G, op.V, 1)
		rows[op.ID] = gdelta.Row{G: op.G, V: op.V}
	}
	if len(aggs) > maxGroups {
		return nil, ErrTooManyGroups
	}
	return cs, nil
}

// View returns a copy of the materialized view (groups with count > 0).
func (s *Store) View() map[string]gdelta.Agg {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]gdelta.Agg, len(s.aggs))
	for g, a := range s.aggs {
		out[g] = a
	}
	return out
}
