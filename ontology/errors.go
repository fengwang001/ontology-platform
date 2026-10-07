package ontology

import "errors"

var (
	// ErrStartObjectNotFound: the traversal start object does not exist.
	ErrStartObjectNotFound = errors.New("ontology: start object not found")
	// ErrEmptyOrUndefinedLinkTypes: direction set is empty or contains a
	// link type that is not defined on the graph.
	ErrEmptyOrUndefinedLinkTypes = errors.New("ontology: empty direction set or undefined link type")
	// ErrInvalidMaxDepth: depth limit is not a positive integer.
	ErrInvalidMaxDepth = errors.New("ontology: max depth must be a positive integer")
)

var (
	// ErrDuplicateObject is returned when adding an object twice.
	ErrDuplicateObject = errors.New("ontology: duplicate object")
	// ErrObjectNotFound is returned when a link references a missing object.
	ErrObjectNotFound = errors.New("ontology: object not found")
	// ErrDuplicateLink is returned when adding a link with an existing ID.
	ErrDuplicateLink = errors.New("ontology: duplicate link id")
	// ErrInvalidDirection is returned for an unknown Direction value.
	ErrInvalidDirection = errors.New("ontology: invalid direction")
	// ErrLinkNotFound is returned when deleting a missing link id.
	ErrLinkNotFound = errors.New("ontology: link not found")
)
