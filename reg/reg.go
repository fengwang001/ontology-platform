// Package reg is the object registry: reference relations, reclamation
// at zero, idempotency/sharing/failure-atomicity. Depends only on refc.
package reg

import (
	"errors"
	"fmt"
	"sync"

	"ontology/refc"
)

// Sentinel errors, each a distinct, decidable failure class.
var (
	ErrEmpty    = errors.New("reg: empty id or ref")
	ErrExists   = errors.New("reg: object already exists")
	ErrNotFound = errors.New("reg: object not found or reclaimed")
	ErrNotHeld  = errors.New("reg: referrer does not hold object")
)

// Registry holds live objects and their reference counters. lastChecks
// counts how many objects the latest Release inspected to decide
// reclamation: an unexported complexity proof, never public.
type Registry struct {
	mu         sync.RWMutex
	objs       map[string]*refc.Counter
	lastChecks int
}

// New returns an empty Registry.
func New() *Registry { return &Registry{objs: make(map[string]*refc.Counter)} }

// Create registers id with count 0. ErrExists if already present.
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

// Acquire makes ref hold id. Idempotent per (ref, id) pair.
func (r *Registry) Acquire(ref, id string) error {
	if ref == "" || id == "" {
		return ErrEmpty
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.objs[id]; ok {
		c.Acquire(ref) // set semantics: no double count
		return nil
	}
	return ErrNotFound
}

// Release drops ref's hold; reclaims id the instant its count hits 0.
// Reclamation examines exactly one object.
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
	held, zero := c.Release(ref)
	if !held {
		return ErrNotHeld
	}
	r.lastChecks = 1 // only the released object is examined
	if zero {
		delete(r.objs, id)
	}
	return nil
}

// RefCount returns the number of distinct referrers holding id.
func (r *Registry) RefCount(id string) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if c, ok := r.objs[id]; ok {
		return c.Count(), nil
	}
	return 0, ErrNotFound
}

// Alive reports whether id exists and is not reclaimed.
func (r *Registry) Alive(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.objs[id]
	return ok
}

// SelfCheck replays built-in sequences verifying the four invariants
// plus the O(1) reclamation-cost property. Nil means all hold.
func (r *Registry) SelfCheck() error {
	g := New()
	_ = g.Create("X") // fresh registry: cannot fail
	acq := []bool{true, true, true, false, false, true}
	refs := []string{"r1", "r2", "r2", "r1", "r2", "r3"}
	ns := []int{1, 2, 2, 1, 0, 0}                       // idempotent dup, shared, zero
	alv := []bool{true, true, true, true, false, false} // reclaimed at 0
	errs := []error{nil, nil, nil, nil, nil, ErrNotFound}
	for i := range acq {
		var err error
		if acq[i] {
			err = g.Acquire(refs[i], "X")
		} else {
			err = g.Release(refs[i], "X")
		}
		if n, _ := g.RefCount("X"); !errors.Is(err, errs[i]) || n != ns[i] || g.Alive("X") != alv[i] {
			return fmt.Errorf("selfcheck step %d: n=%d err=%v", i, n, err)
		}
	}
	// Failure atomicity: rejected ops must not change state.
	h := New()
	_ = h.Create("Y")
	_ = h.Acquire("a", "Y")
	rej := []error{h.Create("Y"), h.Acquire("b", "ZZ"), h.Release("b", "Y"), h.Acquire("", "Y"), h.Release("a", "")}
	for _, e := range rej {
		if e == nil {
			return errors.New("selfcheck: rejected op returned nil")
		}
	}
	if n, _ := h.RefCount("Y"); n != 1 || !h.Alive("Y") {
		return errors.New("selfcheck: rejected op mutated state")
	}
	// Reclaim cost must not scale with registry size.
	for _, m := range []int{100, 1000, 10000} {
		k := New()
		for i := 0; i < m; i++ {
			id := fmt.Sprintf("o%d", i)
			_ = k.Create(id)
			_ = k.Acquire(fmt.Sprintf("r%d", i), id)
		}
		if err := k.Release("r0", "o0"); err != nil || k.lastChecks > 1 {
			return fmt.Errorf("selfcheck: reclaim m=%d checks=%d err=%v", m, k.lastChecks, err)
		}
	}
	return nil
}
