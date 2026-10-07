// Package temporal implements a time-travel sub-system for the ontology
// platform: an append-only history of objects, links, object-type property
// definitions and link-type cardinality constraints, together with
// snapshot-scoped graph traversal anchored at an exact historical instant.
package temporal

// Instant is a logical, monotonically increasing commit time. Each committed
// transaction receives one instant; a snapshot at instant t observes exactly
// the effects of commits 1..t.
type Instant int64

// Property is one attribute definition attached to an object type.
type Property struct {
	Name string
	Type string
}

// Cardinality constrains how many links of a given link type may emanate from
// one source object. Only the rule versioned for the traversal's fixed
// instant is ever consulted by a traversal.
type Cardinality struct {
	MaxOut int // -1 means unbounded
}

// ObjectID, LinkTypeID and TypeID are stable identifiers.
type (
	ObjectID   string
	TypeID     string
	LinkTypeID string
)

// PropertyValues maps property name to the value held at some instant.
// Values are treated opaquely (compared with reflect.DeepEqual).
type PropertyValues map[string]any
