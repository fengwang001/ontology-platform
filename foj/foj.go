// Package foj maintains per-key incremental FULL OUTER JOIN state.
package foj

import (
	"errors"
	"maps"
	"slices"
	"strconv"
	"sync"

	"ontology/rel"
)

// ErrEmptyKey: the join key is the empty string.
var ErrEmptyKey = errors.New("foj: empty key")

// Kind identifies the operation type.
type Kind int

const (
	PutLeft Kind = iota
	PutRight
	DelLeft
	DelRight
)

// Op is one input operation.
type Op struct {
	Kind Kind
	Key  string
	ID   string
}

// Engine keeps one rel.Rel per key and applies ops incrementally.
type Engine struct {
	mu      sync.RWMutex
	rels    map[string]*rel.Rel
	checked int // distinct keys examined by the most recent Apply
}

// New returns an empty Engine.
func New() *Engine { return &Engine{rels: map[string]*rel.Rel{}} }

// Apply applies a batch of ops atomically: on any rejection nothing changes
// and a nil change log is returned.
func (e *Engine) Apply(ops ...Op) ([]rel.Change, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.checked = 0
	seen := map[string]bool{}
	backup := map[string]*rel.Rel{}
	rollback := func(err error) ([]rel.Change, error) {
		for k, b := range backup {
			if b == nil {
				delete(e.rels, k)
			} else {
				e.rels[k] = b
			}
		}
		return nil, err
	}
	var out []rel.Change
	for _, op := range ops {
		if op.Key == "" {
			return rollback(ErrEmptyKey)
		}
		r, ok := e.rels[op.Key]
		if !seen[op.Key] { // hash-locate each touched key exactly once
			seen[op.Key] = true
			e.checked++
			if ok {
				backup[op.Key] = r.Clone()
			} else {
				backup[op.Key] = nil
			}
		}
		if !ok {
			r = rel.New(op.Key)
			e.rels[op.Key] = r
		}
		var ch []rel.Change
		var err error
		switch op.Kind {
		case PutLeft:
			ch, err = r.PutLeft(op.ID)
		case PutRight:
			ch, err = r.PutRight(op.ID)
		case DelLeft:
			ch, err = r.DelLeft(op.ID)
		case DelRight:
			ch, err = r.DelRight(op.ID)
		}
		if err != nil {
			return rollback(err)
		}
		out = append(out, ch...)
	}
	for k := range seen { // drop keys that became empty; only touched keys
		if e.rels[k].Empty() {
			delete(e.rels, k)
		}
	}
	return out, nil
}

// View returns the full materialized view, sorted by key then row.
func (e *Engine) View() []rel.Row {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var out []rel.Row
	for _, k := range slices.Sorted(maps.Keys(e.rels)) {
		out = append(out, e.rels[k].View()...)
	}
	return out
}

// ScalingHolds reports whether the number of keys examined per Apply stays
// bounded by a small constant independent of the total key count m. It never
// exposes the counter value itself.
func ScalingHolds(ms ...int) bool {
	for _, m := range ms {
		e := New()
		for i := 0; i < m; i++ {
			if _, err := e.Apply(Op{PutLeft, "k" + strconv.Itoa(i), "l"}); err != nil {
				return false
			}
		}
		if _, err := e.Apply(Op{PutRight, "k0", "r"}); err != nil {
			return false
		}
		if e.checked > 2 {
			return false
		}
	}
	return true
}
