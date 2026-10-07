// Package ontology provides a minimal common ontology model (object types,
// directed link types, existence-based permissions) together with a
// point-in-time subgraph snapshot extractor.
package ontology

// ObjectID is the global identifier of an object.
type ObjectID string

// TypeName is the name of an object type or link type.
type TypeName string

// Principal is a caller identity used for permission checks.
type Principal string

// ObjectType defines a kind of object in the ontology.
type ObjectType struct {
	Name TypeName
}

// Direction enumerates the direction semantics of a link type.
type Direction int

const (
	// Directed links are one-way: from Source to Sink.
	Directed Direction = iota
	// Undirected links have no orientation; Source/Sink are canonicalized.
	Undirected
)

// LinkType defines a kind of directed (or undirected) link between objects.
type LinkType struct {
	Name   TypeName
	Source TypeName
	Sink   TypeName
	Direct Direction
}

// Object is an instance of an ObjectType.
type Object struct {
	ID      ObjectID
	Type    TypeName
	Readers []Principal
}

// Link is an instance of a LinkType connecting two objects.
type Link struct {
	Type   TypeName
	Source ObjectID
	Sink   ObjectID
}

// DanglingSource explains why a boundary link was excluded from a snapshot.
type DanglingSource int

const (
	// DanglingByScope means the remote end was never in the requested scope.
	DanglingByScope DanglingSource = iota + 1
	// DanglingByPermission means the remote end was in scope but was removed
	// because the caller lacks existence permission on it.
	DanglingByPermission
)

// DanglingLink is an attachment-record describing a link excluded from the
// snapshot body: exactly one end is included in the snapshot.
type DanglingLink struct {
	Type TypeName
	// LinkDirection is the direction of the owning link type.
	LinkDirection Direction
	LocalEnd      ObjectID
	RemoteEnd     ObjectID
	// RemoteRedacted is true when the remote id is withheld because the
	// caller lacks existence permission on the remote object.
	RemoteRedacted bool
	Source         DanglingSource
}

// ChangeKind enumerates journaled graph mutations.
type ChangeKind int

const (
	ObjectAdded ChangeKind = iota + 1
	ObjectRemoved
	LinkAdded
	LinkRemoved
)

// Change is one journaled mutation, tagged with the epoch in which it happened.
type Change struct {
	Epoch  uint64
	Kind   ChangeKind
	Object ObjectID
	Link   *Link
}
