// Package ontology implements event-sourced reconstruction of an
// object/link network and retroactive "should have been orphaned"
// adjudication.
package ontology

// EventKind enumerates the event-sourced record types.
type EventKind int

const (
	// EvLinkTypeDeclared registers a link type in the global schema.
	EvLinkTypeDeclared EventKind = iota + 1
	// EvObjectCreated creates an object of a given object type.
	EvObjectCreated
	// EvLinkEstablished activates a typed edge between two objects.
	EvLinkEstablished
	// EvLinkRevoked deactivates a previously established edge.
	EvLinkRevoked
	// EvPropertyAssigned writes a named property on an object.
	EvPropertyAssigned
	// EvOrphanMarked is the explicit, on-stream cascade-cleanup record.
	EvOrphanMarked
)

// OrderKey totally orders committed records: timestamp first, then the
// global commit sequence number as the authoritative tie breaker.
type OrderKey struct {
	Time int64
	Seq  uint64
}

// Before reports whether k precedes o.
func (k OrderKey) Before(o OrderKey) bool {
	if k.Time != o.Time {
		return k.Time < o.Time
	}
	return k.Seq < o.Seq
}

// After reports whether k follows o.
func (k OrderKey) After(o OrderKey) bool { return o.Before(k) }

// Event is one committed event-sourced record.
type Event struct {
	Kind EventKind
	Time int64
	Seq  uint64
	// ObjectID is the subject object (created object, edge endpoint...).
	ObjectID string
	// TypeID is the object type (create) or link type name.
	TypeID string
	// PeerID is the other endpoint of an edge.
	PeerID string
	// PropertyKey / PropertyValue carry property assignments.
	PropertyKey   string
	PropertyValue string
}

// Key returns the event's total order position.
func (e Event) Key() OrderKey { return OrderKey{Time: e.Time, Seq: e.Seq} }

// EventInput is an append request; Seq is assigned by the store.
type EventInput struct {
	Kind          EventKind
	Time          int64
	ObjectID      string
	TypeID        string
	PeerID        string
	PropertyKey   string
	PropertyValue string
}

// ObjectStatus classifies an object in a reconstructed network.
type ObjectStatus int

const (
	// StatusActive: live according to the pinned rule version.
	StatusActive ObjectStatus = iota
	// StatusCascadeOrphan: an explicit OrphanMarked record exists.
	StatusCascadeOrphan
	// StatusRetroactiveOrphan: the pinned rule says it should have been
	// cascade-cleaned, but no OrphanMarked record exists.
	StatusRetroactiveOrphan
)

// SubsequentActivity is a normal event observed after the virtual (or
// actual) orphan point.
type SubsequentActivity struct {
	Key    OrderKey
	Kind   EventKind
	Detail string
}

// ObjectState is the reconstructed state of one object at time T.
type ObjectState struct {
	ID                   string
	ObjectType           string
	CreatedAt            OrderKey
	Properties           map[string]string
	ActiveLinks          map[EdgeKey]struct{}
	Status               ObjectStatus
	VirtualOrphanAt      OrderKey
	MarkedOrphanAt       OrderKey
	HasVirtualPoint      bool
	HasMarkedRecord      bool
	ActivityAfterVirtual []SubsequentActivity
	ActivityAfterMarked  []SubsequentActivity
}

// EdgeKey identifies a typed edge.
type EdgeKey struct {
	LinkType string
	From     string
	To       string
}

// NetworkState is the whole-network reconstruction at one timestamp.
type NetworkState struct {
	At      int64
	Objects map[string]*ObjectState
	// Problems records per-object adjudication errors (dangling
	// references, ambiguous ties, ...). It never blocks other objects.
	Problems map[string]error
}
