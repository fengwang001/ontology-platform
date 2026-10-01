package itc

import "fmt"

func (s stamp) String() string {
	return "(" + s.id.String() + ";" + s.event.String() + ")"
}

func (rel Relation) String() string {
	switch rel {
	case Before:
		return "Before"
	case After:
		return "After"
	case Concurrent:
		return "Concurrent"
	default:
		return "Equal"
	}
}

// NewRegistry creates an empty replica registry.
func NewRegistry() *Registry {
	return &Registry{replicas: make(map[string]stamp)}
}

// Seed creates a fresh seed replica named name with stamp (1;0).
func (r *Registry) Seed(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if name == "" {
		return ErrEmptyName
	}
	if _, ok := r.replicas[name]; ok {
		return ErrNameExists
	}
	r.replicas[name] = stamp{id: idOne(), event: evInt(0)}
	return nil
}

// Fork splits the identity of name into two disjoint parts, assigning the
// first part to name and the second to child. Both keep the same event tree.
// It returns the stamps of name and child, in that order.
func (r *Registry) Fork(name, child string) (string, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if name == "" || child == "" {
		return "", "", ErrEmptyName
	}
	parent, ok := r.replicas[name]
	if !ok {
		return "", "", fmt.Errorf("%w: %q", ErrUnknownName, name)
	}
	if _, exists := r.replicas[child]; exists {
		return "", "", fmt.Errorf("%w: %q", ErrNameExists, child)
	}
	l, rr := idFork(parent.id)
	parent.id = l
	r.replicas[name] = parent
	r.replicas[child] = stamp{id: rr, event: evClone(parent.event)}
	return parent.String(), r.replicas[child].String(), nil
}

// Event increments the event tree of name.
func (r *Registry) Event(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if name == "" {
		return ErrEmptyName
	}
	s, ok := r.replicas[name]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownName, name)
	}
	filled := evFill(s.id, evClone(s.event))
	if !evEqual(filled, s.event) {
		s.event = filled
	} else {
		grown, _ := evGrow(s.id, evClone(s.event))
		s.event = grown
	}
	r.replicas[name] = s
	return nil
}

// Peek returns a snapshot of name as a stamp with identity 0: "(0;event)".
// The returned string is immutable and never aliases internal state.
func (r *Registry) Peek(name string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if name == "" {
		return "", ErrEmptyName
	}
	s, ok := r.replicas[name]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrUnknownName, name)
	}
	return stamp{id: idZero(), event: s.event}.String(), nil
}

// Join merges b into a (summing identities, joining events) and removes b.
func (r *Registry) Join(a, b string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if a == "" || b == "" {
		return "", ErrEmptyName
	}
	if a == b {
		return "", ErrSameName
	}
	sa, ok := r.replicas[a]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrUnknownName, a)
	}
	sb, ok := r.replicas[b]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrUnknownName, b)
	}
	sum, ok := idSum(sa.id, sb.id)
	if !ok {
		return "", fmt.Errorf("%w: %q and %q", ErrIdentityOverlap, a, b)
	}
	sa.id = sum
	sa.event = evJoin(sa.event, sb.event)
	r.replicas[a] = sa
	delete(r.replicas, b)
	return sa.String(), nil
}

// Compare reports the causal relation between the events of a and b.
func (r *Registry) Compare(a, b string) (Relation, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if a == "" || b == "" {
		return Equal, ErrEmptyName
	}
	sa, ok := r.replicas[a]
	if !ok {
		return Equal, fmt.Errorf("%w: %q", ErrUnknownName, a)
	}
	sb, ok := r.replicas[b]
	if !ok {
		return Equal, fmt.Errorf("%w: %q", ErrUnknownName, b)
	}
	ab := evLeq(sa.event, sb.event)
	ba := evLeq(sb.event, sa.event)
	switch {
	case ab && ba:
		return Equal, nil
	case ab:
		return Before, nil
	case ba:
		return After, nil
	default:
		return Concurrent, nil
	}
}

// String renders the stamp of name exactly as "(id;event)".
func (r *Registry) String(name string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if name == "" {
		return "", ErrEmptyName
	}
	s, ok := r.replicas[name]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrUnknownName, name)
	}
	return s.String(), nil
}
