package ontology

import "errors"

// Sentinel errors returned by the consistent hash ring. They are stable,
// comparable values so callers can discriminate failure modes with errors.Is.
var (
	// ErrEmptyRing is returned by Locate when no node has been added yet.
	ErrEmptyRing = errors.New("ontology: ring is empty")

	// ErrInvalidVnodes is returned by Add when vnodes is not positive.
	ErrInvalidVnodes = errors.New("ontology: vnodes must be greater than zero")

	// ErrNodeExists is returned by Add when the node ID is already present.
	ErrNodeExists = errors.New("ontology: node already exists")

	// ErrNodeNotFound is returned by Remove when the node ID is absent.
	ErrNodeNotFound = errors.New("ontology: node not found")
)
