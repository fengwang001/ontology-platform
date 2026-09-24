// Package semi incrementally maintains a semi-join view: a left row is
// retained iff its non-NULL key has a right-table refcount >= 1.
package semi

import (
	"errors"
	"slices"
	"sync"

	"ontology/key"
)

var (
	ErrBadMaxLeft   = errors.New("semi: maxLeft must be positive")
	ErrLeftExists   = errors.New("semi: left id already exists")
	ErrLeftNotFound = errors.New("semi: left id not found")
	ErrTooManyLeft  = errors.New("semi: left row limit exceeded")
	ErrRefNegative  = errors.New("semi: right refcount would go negative")
)

// Engine holds the left registry, right refcounts and the retained set.
type Engine struct {
	mu       sync.RWMutex
	maxLeft  int
	left     map[int64]*string             // id -> key (nil = NULL)
	byKey    map[string]map[int64]struct{} // non-NULL key -> left ids
	ref      map[string]int                // non-NULL key -> refcount
	nilRef   int                           // NULL right rows: counted, never match
	retained map[int64]struct{}            // ids currently in the view
	checked  int                           // left rows examined by last AddRight/DelRight
}

// New creates an Engine holding at most maxLeft left rows.
func New(maxLeft int) (*Engine, error) {
	if maxLeft <= 0 {
		return nil, ErrBadMaxLeft
	}
	e := &Engine{maxLeft: maxLeft}
	e.left, e.ref = map[int64]*string{}, map[string]int{}
	e.byKey, e.retained = map[string]map[int64]struct{}{}, map[int64]struct{}{}
	return e, nil
}

// AddLeft inserts a left row; the id must be fresh and within the limit.
func (e *Engine) AddLeft(id int64, k *string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, dup := e.left[id]; dup {
		return ErrLeftExists
	}
	if len(e.left) >= e.maxLeft {
		return ErrTooManyLeft
	}
	e.left[id] = k
	if v, ok := key.Norm(k); ok {
		if e.byKey[v] == nil {
			e.byKey[v] = map[int64]struct{}{}
		}
		e.byKey[v][id] = struct{}{}
		if e.ref[v] >= 1 {
			e.retained[id] = struct{}{}
		}
	}
	return nil
}

// DelLeft removes a left row; unknown ids are rejected without side effects.
func (e *Engine) DelLeft(id int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	k, ok := e.left[id]
	if !ok {
		return ErrLeftNotFound
	}
	delete(e.left, id)
	delete(e.retained, id)
	if v, ok := key.Norm(k); ok {
		delete(e.byKey[v], id)
	}
	return nil
}

// AddRight increments the key's refcount.
func (e *Engine) AddRight(k *string) error { return e.bump(k, 1) }

// DelRight decrements the key's refcount; going negative is rejected atomically.
func (e *Engine) DelRight(k *string) error { return e.bump(k, -1) }

// bump applies a refcount delta and flips affected rows on 0->1 / 1->0 edges.
func (e *Engine) bump(k *string, d int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.checked = 0
	v, ok := key.Norm(k)
	if !ok {
		if e.nilRef+d < 0 {
			return ErrRefNegative
		}
		e.nilRef += d
		return nil
	}
	if e.ref[v]+d < 0 {
		return ErrRefNegative
	}
	e.ref[v] += d
	switch {
	case d == 1 && e.ref[v] == 1: // 0->1: light up, O(rows under this key)
		for id := range e.byKey[v] {
			e.retained[id] = struct{}{}
			e.checked++
		}
	case d == -1 && e.ref[v] == 0: // 1->0: extinguish
		for id := range e.byKey[v] {
			delete(e.retained, id)
			e.checked++
		}
	}
	return nil
}

// View returns the retained left ids in ascending order.
func (e *Engine) View() []int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]int64, 0, len(e.retained))
	for id := range e.retained {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// BatchView recomputes the view from scratch: the reference for invariant 1.
func (e *Engine) BatchView() []int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := []int64{}
	for id, k := range e.left {
		if v, ok := key.Norm(k); ok && e.ref[v] >= 1 {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}
