package ontology

import "errors"

// Configuration errors returned by NewLimiter.
var (
	// ErrInvalidCapacity indicates a non-positive Capacity.
	ErrInvalidCapacity = errors.New("ontology: capacity must be positive")
	// ErrInvalidRate indicates a non-positive Rate.
	ErrInvalidRate = errors.New("ontology: rate must be positive")
)
