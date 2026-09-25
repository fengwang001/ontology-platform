// Package router implements partition affinity routing, drain and atomic migration over part; one RWMutex makes Migrate atomic.
package router

import (
	"errors"
	"fmt"
	"sync"

	"ontology/part"
)

var (
	ErrPartitionNotFound = errors.New("router: partition does not exist")
	ErrPartitionExists   = errors.New("router: partition already exists")
	ErrAffinityConflict  = errors.New("router: target partition is not Active")
	ErrNoAffinity        = errors.New("router: key has no affinity partition")
	ErrInvalidMigrate    = errors.New("router: invalid migrate arguments")
	ErrEmptyKey          = errors.New("router: key must be non-empty")
	ErrAlreadyAssigned   = errors.New("router: key is already assigned")
)

type Router struct {
	mu                 sync.RWMutex
	parts              map[int]*part.Partition
	affinity           map[string]int // key -> owner after every completed Migrate
	values             map[string]string
	accepted           int
	lastMigrateScanned int
}

func New() *Router {
	return &Router{parts: map[int]*part.Partition{}, affinity: map[string]int{}, values: map[string]string{}}
}
func (r *Router) AddPartition(id int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.parts[id]; ok {
		return ErrPartitionExists
	}
	r.parts[id] = part.New()
	return nil
}

func (r *Router) Assign(key string, p int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	pn, ok := r.parts[p]
	_, dup := r.affinity[key]
	switch {
	case key == "":
		return ErrEmptyKey
	case dup:
		return ErrAlreadyAssigned
	case !ok:
		return ErrPartitionNotFound
	case pn.State() != part.Active:
		return ErrAffinityConflict
	}
	r.affinity[key] = p
	pn.Add(key)
	r.accepted++
	return nil
}
func (r *Router) Put(key, val string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.affinity[key]
	if !ok {
		return ErrNoAffinity
	}
	if r.parts[p].State() != part.Active {
		return ErrAffinityConflict
	}
	r.values[key] = val
	return nil
}
func (r *Router) Get(key string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, ok := r.affinity[key]; !ok {
		return "", false
	}
	v, ok := r.values[key]
	return v, ok
}
func (r *Router) BeginDrain(p int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	pn, ok := r.parts[p]
	if !ok {
		return ErrPartitionNotFound
	}
	return pn.BeginDrain()
}

// Migrate moves all keys of Draining from to Active to atomically, marks from Removed (inv.2); TakeAll clears from (甲 cannot occur); only from inspected => scanned 1 for any m.
func (r *Router) Migrate(from, to int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	src, sok := r.parts[from]
	dst, dok := r.parts[to]
	switch {
	case !sok || !dok:
		return ErrPartitionNotFound
	case from == to || src.State() != part.Draining || dst.State() != part.Active:
		return ErrInvalidMigrate
	}
	for _, k := range src.TakeAll() {
		dst.Add(k)
		r.affinity[k] = to
	}
	r.lastMigrateScanned = 1
	return src.MarkRemoved()
}
func (r *Router) Count(p int) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if pn, ok := r.parts[p]; ok {
		return pn.Len()
	}
	return 0
}

// SelfCheck recounts from accepted Assigns + completed Migrate redirects and compares to owner sets/counts and accepted total (inv. 1,2).
func (r *Router) SelfCheck() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	recount := map[int]int{}
	for k, p := range r.affinity {
		pn := r.parts[p]
		if pn == nil || pn.State() == part.Removed || !pn.Has(k) {
			return fmt.Errorf("key %q wrongly owned by %d", k, p)
		}
		recount[p]++
	}
	for id, pn := range r.parts {
		if pn.Len() != recount[id] || (pn.State() == part.Removed && pn.Len() != 0) {
			return fmt.Errorf("partition %d count/state wrong", id)
		}
	}
	if len(r.affinity) != r.accepted {
		return fmt.Errorf("total %d != accepted %d", len(r.affinity), r.accepted)
	}
	for k := range r.values {
		if _, ok := r.affinity[k]; !ok {
			return fmt.Errorf("value for unaffinitized key %q", k)
		}
	}
	return nil
}
