// Package gc implements an object cascade-deletion controller with owner
// references and finalizers, modeled after Kubernetes garbage collection
// semantics but fully synchronous and deterministic.
package gc

import "time"

// Policy is the deletion propagation strategy requested for an object.
type Policy int

const (
	// PolicyBackground removes the object as soon as its finalizers are
	// empty; dependents that lose their last owner are deleted in background.
	PolicyBackground Policy = iota
	// PolicyForeground additionally waits until no live blocking dependent
	// remains, and propagates foreground deletion to eligible dependents.
	PolicyForeground
	// PolicyOrphan removes the object and detaches its dependents, which are
	// kept alive even if they end up with no owners at all.
	PolicyOrphan
)

// Valid reports whether p is one of the three defined policies.
func (p Policy) Valid() bool {
	return p == PolicyBackground || p == PolicyForeground || p == PolicyOrphan
}

func (p Policy) String() string {
	switch p {
	case PolicyBackground:
		return "background"
	case PolicyForeground:
		return "foreground"
	case PolicyOrphan:
		return "orphan"
	}
	return "unknown"
}

// OwnerReference points from a dependent object to one of its owners.
// Block reports whether this reference blocks a foreground deletion of the
// owner while the dependent is still alive.
type OwnerReference struct {
	ID    string
	Block bool
}

// Object is a managed entity with a globally unique non-empty ID.
type Object struct {
	ID         string
	Owners     []OwnerReference
	Finalizers []string

	// Deleting is set by the first accepted Delete call.
	Deleting bool
	// Policy is the recorded deletion policy, meaningful when Deleting.
	Policy Policy
	// DeletionRequestedAt is the moment the deletion was requested,
	// meaningful when Deleting.
	DeletionRequestedAt time.Time
}

func (o *Object) clone() *Object {
	cp := *o
	cp.Owners = append([]OwnerReference(nil), o.Owners...)
	cp.Finalizers = append([]string(nil), o.Finalizers...)
	return &cp
}

// EventKind classifies the internal state transitions emitted during
// convergence. Observers may use events to explain or audit a cascade.
type EventKind int

const (
	// EventMarkDeleting: an object entered the deleting state.
	EventMarkDeleting EventKind = iota
	// EventUpgradeForeground: a background-deleting object was upgraded to
	// foreground (by a delete request or by foreground propagation).
	EventUpgradeForeground
	// EventRemove: an object was physically removed.
	EventRemove
	// EventDetach: an owner reference was detached because the owner was
	// removed. ID is the dependent, Owner is the removed owner.
	EventDetach
)

// Event describes one atomic state transition performed by the controller.
type Event struct {
	Kind   EventKind
	ID     string
	Owner  string
	Policy Policy
}

// Stats counts the elementary work performed by the last mutating
// operation. It exists so tests can prove that the cost of an operation
// depends only on the objects and references it actually affects.
type Stats struct {
	PropagationChecks int
	RemovalChecks     int
	EdgesWalked       int
}
