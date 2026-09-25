// Package dd holds the row index (rowID -> current tuple), the retract
// logic of Upsert/Delete and the incrementally maintained distinct counts.
// It depends only on tup. All state is guarded by one mutex.
package dd

import (
	"errors"
	"sync"

	"ontology/tup"
)

// Sentinel errors are mutually distinct and detectable with errors.Is.
var (
	ErrRowIDNotPositive = errors.New("dd: rowID must be a positive integer")
	ErrEmptyKey         = errors.New("dd: key must not be the empty string")
	ErrRowNotFound      = errors.New("dd: delete target rowID does not exist")
)

type row struct {
	key string
	t   tup.T
}

// Engine is the in-process index. The unexported checks field counts tuples
// inspected while updating distinct counts during Upsert/Delete; it is not
// reachable through any exported method.
type Engine struct {
	mu     sync.RWMutex
	rows   map[int]row
	tab    *tup.Table
	checks int
}

// New returns an empty engine.
func New() *Engine {
	return &Engine{rows: make(map[int]row), tab: tup.NewTable()}
}

// Upsert inserts rowID or moves it to a new tuple. An existing row first
// retracts its old tuple (exactly -1) and then adds the new tuple (exactly
// +1). Invalid arguments are rejected before any state changes.
func (e *Engine) Upsert(rowID int, key, col1 string, col2 int) error {
	if rowID <= 0 {
		return ErrRowIDNotPositive
	}
	if key == "" {
		return ErrEmptyKey
	}
	nt := tup.T{C1: col1, C2: col2}
	e.mu.Lock()
	defer e.mu.Unlock()
	old, ok := e.rows[rowID]
	switch {
	case ok && old.key == key && old.t == nt:
		// Identical row: nothing to retract or add.
	case ok:
		e.tab.Get(old.key).Remove(old.t)
		e.tab.Get(key).Add(nt)
		e.checks += 2
	default:
		e.tab.Get(key).Add(nt)
		e.checks++
	}
	e.rows[rowID] = row{key: key, t: nt}
	return nil
}

// Delete retracts the row's current tuple (exactly -1) and removes the row.
// A missing rowID is rejected without touching state.
func (e *Engine) Delete(rowID int) error {
	if rowID <= 0 {
		return ErrRowIDNotPositive
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.rows[rowID]
	if !ok {
		return ErrRowNotFound
	}
	e.tab.Get(r.key).Remove(r.t)
	e.checks++
	delete(e.rows, rowID)
	return nil
}

// Distinct returns the number of distinct (Col1,Col2) tuples held by active
// rows in key, which is 0 for an unknown key.
func (e *Engine) Distinct(key string) int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if g, ok := e.tab.Lookup(key); ok {
		return g.Distinct()
	}
	return 0
}

// Total returns the sum of per-group distinct counts.
func (e *Engine) Total() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	sum := 0
	e.tab.Groups(func(_ string, g *tup.Group) { sum += g.Distinct() })
	return sum
}
