package ontology

import "errors"

// Version is a strictly monotonic per-instance version number. Version 0
// denotes the pre-create baseline.
type Version int64

// Property is a property name within an object instance.
type Property string

// ObjectID identifies an object instance inside a Store.
type ObjectID string

// TypeName identifies an object type whose validators are registered on it.
type TypeName string

// Value is a single property value; JSON round-tripping must be stable.
type Value any

// ErrRejected is returned (wrapped by Result.Err) when a write fails
// client-side validation. It is distinct from the three conflict kinds.
var ErrRejected = errors.New("ontology: write rejected by validator")

// ConflictKind enumerates the three mutually exclusive rejection classes.
type ConflictKind int

const (
	ConflictNone ConflictKind = iota
	// ConflictDeleted: the target instance is logically deleted.
	ConflictDeleted
	// ConflictStaleBase: the declared baseline does not match the current
	// head (unknown / ahead-of-head baseline) or the IdempotencyKey replays
	// an already decided write.
	ConflictStaleBase
	// ConflictProperty: the write set union declared read scope intersects a
	// footprint committed since the declared baseline (locally or via linked
	// instances observed through validator read scopes).
	ConflictProperty
)

// PropertyRef identifies a property on an instance. Instance == "" means a
// property on the write's target instance; otherwise it identifies a linked
// instance reached via the Link field of the target.
type PropertyRef struct {
	Instance ObjectID
	Link     Property
	Local    Property
}

// ReadScope declares everything a validator reads for a given write. Refs
// describe property-level reads; ExternalVersions optionally pins the exact
// versions of linked instances that were observed client-side.
type ReadScope struct {
	Refs             []PropertyRef
	ExternalVersions map[ObjectID]Version
}

// Validator is a validation hook registered on an object type. Declare is
// invoked at commit time (under the store's resolution lock) with the
// proposed property values and the resolved target snapshot; it returns the
// fields the hook reads. Validate, when non-nil, performs the actual check
// and its returned error fails the write with ErrRejected without advancing
// any state.
type Validator struct {
	Name     string
	Declare  func(values map[Property]Value, snap Snapshot) ReadScope
	Validate func(values map[Property]Value, snap Snapshot, linked map[ObjectID]Snapshot) error
}

// WriteRequest is a single commit attempt.
type WriteRequest struct {
	Object         ObjectID
	Type           TypeName
	Base           Version
	IdempotencyKey string
	Values         map[Property]Value
	// ObserveExternal pins linked-instance versions observed by the caller;
	// hooks may add further observations through their scopes.
	ObserveExternal map[ObjectID]Version
	// Create marks an instance-creation write (base must be 0 and absent).
	Create bool
	// Delete marks a logical-delete write.
	Delete bool
}

// Snapshot is an immutable byte-identical view of one committed version.
type Snapshot interface {
	Version() Version
	Deleted() bool
	Get(p Property) (Value, bool)
	Properties() []Property
	Bytes() []byte
}

// Result is the outcome of a commit attempt.
type Result struct {
	Kind       ConflictKind
	Err        error
	NewVersion Version
}

func (r Result) OK() bool { return r.Kind == ConflictNone && r.Err == nil }
