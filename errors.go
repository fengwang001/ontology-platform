package ontology

import "errors"

// Sentinel errors returned by Ring operations. Each is distinguishable
// with errors.Is.
var (
	// ErrEmptyRing is returned by Locate when the ring has no nodes.
	ErrEmptyRing = errors.New("ontology: locate on empty ring")
	// ErrInvalidVnodes is returned by Add when vnodes <= 0.
	ErrInvalidVnodes = errors.New("ontology: vnodes must be positive")
	// ErrNodeExists is returned by Add when the node ID is already present.
	ErrNodeExists = errors.New("ontology: node already exists")
	// ErrNodeNotFound is returned by Remove for an unknown node ID.
	ErrNodeNotFound = errors.New("ontology: node not found")
)
