// Package reg is a thread-safe registry mapping names to
// G-counters. It depends only on package gc.
package reg

import (
	"errors"
	"sync"

	"ontology/gc"
)

// ErrUnknownName is returned when a counter name is not registered.
var ErrUnknownName = errors.New("reg: unknown counter name")

// Registry holds named G-counters guarded by one RWMutex.
type Registry struct {
	mu sync.RWMutex
	m  map[string]*gc.Counter
}

// New returns an empty Registry.
func New() *Registry { return &Registry{m: make(map[string]*gc.Counter)} }

// Set registers name with a counter built from entries. Invalid
// entries (negative node/count) are rejected without side effects.
func (r *Registry) Set(name string, entries map[int]int) error {
	c, err := gc.FromMap(entries)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.m[name] = c
	r.mu.Unlock()
	return nil
}

// Inc adds k to node's entry in the named counter. Validation
// happens before any mutation, so rejected calls leave no trace.
func (r *Registry) Inc(name string, node, k int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.m[name]
	if !ok {
		return ErrUnknownName
	}
	return c.Inc(node, k)
}

// MergeInto replaces nameA with the per-entry max of nameA and
// nameB and returns nameA's new value. Both names must exist; a
// counter with negative entries fails the whole merge atomically.
func (r *Registry) MergeInto(nameA, nameB string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.m[nameA]
	if !ok {
		return 0, ErrUnknownName
	}
	b, ok := r.m[nameB]
	if !ok {
		return 0, ErrUnknownName
	}
	merged, err := gc.Merge(a, b)
	if err != nil {
		return 0, err
	}
	r.m[nameA] = merged
	return gc.Value(merged), nil
}

// Value returns the sum of all entries of the named counter.
func (r *Registry) Value(name string) (int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.m[name]
	if !ok {
		return 0, ErrUnknownName
	}
	return gc.Value(c), nil
}

// Snapshot returns a copy of the named counter's entries.
func (r *Registry) Snapshot(name string) (map[int]int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.m[name]
	if !ok {
		return nil, ErrUnknownName
	}
	return c.Snapshot(), nil
}
