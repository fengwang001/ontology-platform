package ontology

import "errors"

var (
	// ErrInvalidArgument is returned for illegal extraction parameters
	// (empty scope, malformed ids, oversized scope).
	ErrInvalidArgument = errors.New("ontology: invalid argument")
	// ErrNotFound is returned when a referenced graph element does not exist.
	ErrNotFound = errors.New("ontology: not found")
	// ErrTypeMismatch is returned when a link instance violates its type.
	ErrTypeMismatch = errors.New("ontology: type mismatch")
)
