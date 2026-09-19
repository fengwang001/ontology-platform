package ontology

import "errors"

// Sentinel errors returned by Store. Use errors.Is to distinguish them.
var (
	// ErrNegativeLimit is returned when Scan is called with limit < 0.
	// It is an argument error, distinct from an empty page.
	ErrNegativeLimit = errors.New("ontology: limit must not be negative")

	// ErrInvalidCursor is returned when a cursor is malformed, truncated,
	// forged, or fails its integrity check.
	ErrInvalidCursor = errors.New("ontology: cursor is malformed or fails integrity check")

	// ErrInvalidSession is returned when a cursor is well-formed but its
	// traversal session is unknown or has been explicitly invalidated.
	ErrInvalidSession = errors.New("ontology: traversal session is unknown or has been invalidated")
)
