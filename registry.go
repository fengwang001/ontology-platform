package ontology

import (
	"errors"
	"sync"
)

var (
	ErrEmptyName       = errors.New("empty replica name")
	ErrReplicaExists   = errors.New("replica already exists")
	ErrUnknownReplica  = errors.New("unknown replica name")
	ErrSameReplica     = errors.New("both names refer to the same replica")
	ErrIdentityOverlap = errors.New("replica identities overlap")
)

type Comparison string

const (
	Before     Comparison = "Before"
	After      Comparison = "After"
	Equal      Comparison = "Equal"
	Concurrent Comparison = "Concurrent"
)

type stamp struct {
	id    ID
	event Event
}

func (s stamp) String() string {
	return "(" + s.id.String() + ";" + s.event.String() + ")"
}

type Registry struct {
	mu       sync.Mutex
	replicas map[string]stamp
}

func NewRegistry() *Registry {
	return &Registry{replicas: make(map[string]stamp)}
}

func (r *Registry) Seed(name string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if name == "" {
		return "", ErrEmptyName
	}
	if _, exists := r.replicas[name]; exists {
		return "", ErrReplicaExists
	}
	replica := stamp{id: idOne{}, event: eventInt{value: 0}}
	r.replicas[name] = replica
	return replica.String(), nil
}

func (r *Registry) Fork(name, child string) (string, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if name == "" || child == "" {
		return "", "", ErrEmptyName
	}
	parent, ok := r.replicas[name]
	if !ok {
		return "", "", ErrUnknownReplica
	}
	if _, exists := r.replicas[child]; exists {
		return "", "", ErrReplicaExists
	}
	firstID, secondID := forkID(parent.id)
	first := stamp{id: firstID, event: parent.event}
	second := stamp{id: secondID, event: parent.event}
	r.replicas[name] = first
	r.replicas[child] = second
	return first.String(), second.String(), nil
}

func (r *Registry) Event(name string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if name == "" {
		return "", ErrEmptyName
	}
	current, ok := r.replicas[name]
	if !ok {
		return "", ErrUnknownReplica
	}
	filled := fillEvent(current.id, current.event)
	if filled != current.event {
		current.event = filled
	} else {
		current.event, _ = growEvent(current.id, current.event)
	}
	r.replicas[name] = current
	return current.String(), nil
}

func (r *Registry) Peek(name string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if name == "" {
		return "", ErrEmptyName
	}
	current, ok := r.replicas[name]
	if !ok {
		return "", ErrUnknownReplica
	}
	return stamp{id: idZero{}, event: current.event}.String(), nil
}

func (r *Registry) Join(name, other string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if name == "" || other == "" {
		return "", ErrEmptyName
	}
	if name == other {
		return "", ErrSameReplica
	}
	first, ok := r.replicas[name]
	if !ok {
		return "", ErrUnknownReplica
	}
	second, ok := r.replicas[other]
	if !ok {
		return "", ErrUnknownReplica
	}
	mergedID, ok := sumID(first.id, second.id)
	if !ok {
		return "", ErrIdentityOverlap
	}
	merged := stamp{id: mergedID, event: joinEvent(first.event, second.event)}
	r.replicas[name] = merged
	delete(r.replicas, other)
	return merged.String(), nil
}

func (r *Registry) Compare(name, other string) (Comparison, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if name == "" || other == "" {
		return "", ErrEmptyName
	}
	first, ok := r.replicas[name]
	if !ok {
		return "", ErrUnknownReplica
	}
	second, ok := r.replicas[other]
	if !ok {
		return "", ErrUnknownReplica
	}
	firstLE := eventLE(first.event, second.event)
	secondLE := eventLE(second.event, first.event)
	switch {
	case firstLE && secondLE:
		return Equal, nil
	case firstLE:
		return Before, nil
	case secondLE:
		return After, nil
	default:
		return Concurrent, nil
	}
}
