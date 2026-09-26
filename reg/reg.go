// Package reg is a name -> version-vector registry with validation.
package reg

import (
	"errors"
	"sync"

	"ontology/vv"
)

// Sentinel errors for the three distinct, decidable failure modes.
var (
	// ErrNegativeCounter means a Set vector contained counter < 0.
	ErrNegativeCounter = errors.New("reg: counter must be non-negative")
	// ErrNegativeActor means a Set vector contained actor id < 0.
	ErrNegativeActor = errors.New("reg: actor id must be non-negative")
	// ErrUnknownName means Merge/Compare referenced a name never Set.
	ErrUnknownName = errors.New("reg: name is not registered")
)

// Registry stores one version vector per replica name.
type Registry struct {
	mu      sync.RWMutex
	vectors map[string]vv.Vector
}

// New returns an empty registry.
func New() *Registry {
	return &Registry{vectors: make(map[string]vv.Vector)}
}

// Set registers or replaces the named replica's vector. The whole call is
// rejected before any state changes if v contains a negative actor id or a
// negative counter. The stored vector is a deep copy, so later mutation of v
// by the caller cannot corrupt registry state.
func (r *Registry) Set(name string, v vv.Vector) error {
	for actor, counter := range v { // validate everything first
		if actor < 0 {
			return ErrNegativeActor
		}
		if counter < 0 {
			return ErrNegativeCounter
		}
	}
	cp := make(vv.Vector, len(v))
	for actor, counter := range v {
		cp[actor] = counter
	}
	r.mu.Lock()
	r.vectors[name] = cp
	r.mu.Unlock()
	return nil
}

// lookup returns a copy of the named vector under a read lock.
func (r *Registry) lookup(name string) (vv.Vector, error) {
	r.mu.RLock()
	v, ok := r.vectors[name]
	r.mu.RUnlock()
	if !ok {
		return nil, ErrUnknownName
	}
	return v, nil
}

// Merge returns the join of the two named replicas' vectors. It is pure with
// respect to registry state: neither stored vector is modified.
func (r *Registry) Merge(nameA, nameB string) (vv.Vector, error) {
	a, err := r.lookup(nameA)
	if err != nil {
		return nil, err
	}
	b, err := r.lookup(nameB)
	if err != nil {
		return nil, err
	}
	return vv.Merge(a, b), nil
}

// Compare returns the causal relation between two named replicas.
func (r *Registry) Compare(nameA, nameB string) (vv.Relation, error) {
	a, err := r.lookup(nameA)
	if err != nil {
		return vv.Equal, err
	}
	b, err := r.lookup(nameB)
	if err != nil {
		return vv.Equal, err
	}
	return vv.Compare(a, b), nil
}
