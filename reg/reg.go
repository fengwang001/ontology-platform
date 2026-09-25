// Package reg is the object registry: Create/Acquire/Release/RefCount/Alive,
// with exact zero-count reclamation. Depends only on refc.
package reg

import (
	"errors"
	"sort"
	"sync"

	"ontology/refc"
)

// Sentinel errors; each rejected operation maps to exactly one.
var (
	ErrEmpty    = errors.New("empty id or ref")
	ErrExists   = errors.New("object already exists")
	ErrNotFound = errors.New("object not found or reclaimed")
	ErrNotHeld  = errors.New("referrer does not hold the object")
)

// Registry holds all live objects and their reference relations.
type Registry struct {
	mu       sync.RWMutex
	objs     map[string]*refc.Counter
	lastScan int // unexported: objects inspected by the latest Release reclaim check
}

// New returns an empty Registry.
func New() *Registry {
	return &Registry{objs: make(map[string]*refc.Counter)}
}

// Create registers id with count 0. Existing id -> ErrExists.
func (r *Registry) Create(id string) error {
	if id == "" {
		return ErrEmpty
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.objs[id]; ok {
		return ErrExists
	}
	r.objs[id] = refc.New()
	return nil
}

// Acquire lets ref hold id. Idempotent if already held.
func (r *Registry) Acquire(ref, id string) error {
	if ref == "" || id == "" {
		return ErrEmpty
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.objs[id]
	if !ok {
		return ErrNotFound
	}
	c.Inc(ref) // idempotent: no double count
	return nil
}

// Release drops ref's hold; reclaims the object the instant count hits 0.
func (r *Registry) Release(ref, id string) error {
	if ref == "" || id == "" {
		return ErrEmpty
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.objs[id]
	if !ok {
		return ErrNotFound
	}
	held, zero := c.Dec(ref)
	if !held {
		return ErrNotHeld
	}
	r.lastScan = 1 // reclaim check inspects only the released object itself
	if zero {
		delete(r.objs, id) // reclaim at the exact 1->0 moment
	}
	return nil
}

// RefCount returns the number of distinct referrers holding id.
func (r *Registry) RefCount(id string) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.objs[id]
	if !ok {
		return 0, ErrNotFound
	}
	return c.Count(), nil
}

// Alive reports whether id exists and is not reclaimed.
func (r *Registry) Alive(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.objs[id]
	return ok
}

// Holders returns the sorted set of referrers currently holding id.
func (r *Registry) Holders(id string) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.objs[id]
	if !ok {
		return nil, ErrNotFound
	}
	h := c.Holders()
	sort.Strings(h)
	return h, nil
}
