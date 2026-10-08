// Package ontology implements a shortest-path query engine over an
// ontology link graph where object types, link types and a permission
// model jointly constrain reachability and path cost.
package ontology

import "errors"

// Sentinel errors. Use errors.Is to test for them.
var (
	// ErrInvalidParams reports illegal query parameters: unknown start or
	// end object, empty constraint sequence, a repeat marker in a middle
	// position, or an unknown category in the constraint.
	ErrInvalidParams = errors.New("ontology: invalid query parameters")
	// ErrForbiddenType reports that the object type of the start or end
	// object is marked as forbidden for path queries.
	ErrForbiddenType = errors.New("ontology: object type forbidden in path queries")
)

// ObjectID identifies an object instance. Ordering of ObjectID values is
// the plain string ordering and is used for tie-breaking.
type ObjectID string

// LinkID identifies a link instance.
type LinkID string

// LinkTypeID identifies a link type.
type LinkTypeID string

// Principal identifies a permission subject issuing a query.
type Principal string

// ObjectType names an object type from the fixed type set of a Store.
type ObjectType string

// Category names a link category from the fixed, ordered category set of
// a Store. The fixed ordering of categories is the registration order.
type Category string

// MaxLinkCost is the largest allowed per-link step cost.
const MaxLinkCost uint32 = 1_000_000

// LinkType defines the schema of a link kind.
type LinkType struct {
	ID            LinkTypeID
	SrcType       ObjectType
	DstType       ObjectType
	Bidirectional bool
	Category      Category
	Cost          uint32
	// Principals restricts visibility: nil means visible to everyone,
	// otherwise only the listed principals may see links of this type.
	Principals map[Principal]bool
}

// ConstraintPos is one position of a category constraint sequence.
type ConstraintPos struct {
	// Any matches any category; otherwise Category must match exactly.
	Any bool
	// Category is the required category when Any is false.
	Category Category
	// Star marks the position as repeatable zero or more times. Only the
	// first and the last position of the sequence may carry Star.
	Star bool
}

// Query is a shortest-path query.
type Query struct {
	Start      ObjectID
	End        ObjectID
	Principal  Principal
	Constraint []ConstraintPos
}

// Path is a concrete path through the graph.
type Path struct {
	Objects    []ObjectID
	Links      []LinkID
	Categories []Category
	TotalCost  uint64
}

// Result is the outcome of a successful query. A query that is legal but
// has no satisfying path is not an error: Found is false then.
type Result struct {
	Found bool
	// Equivalent is true when at least two distinct optimal paths tie on
	// total cost, category sequence and object sequence. Path then holds
	// the canonical representative (smallest link ID sequence); the
	// choice is deterministic, never random.
	Equivalent bool
	Path       Path
}

// QueryStats is an internal metric counting only the objects and links a
// query actually touched. It is not exposed by Query; tests use it to
// prove that query cost does not grow with unrelated graph size.
type QueryStats struct {
	// ObjectsVisited counts distinct objects whose adjacency was scanned.
	ObjectsVisited int
	// LinksExamined counts adjacency entries inspected during the search.
	LinksExamined int
}
