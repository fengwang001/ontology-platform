package ring

import "errors"

var (
	// ErrEmptyRing is returned by Locate when the ring has no nodes.
	ErrEmptyRing = errors.New("ring: empty ring")
	// ErrInvalidVnodes is returned by Add when vnodes <= 0.
	ErrInvalidVnodes = errors.New("ring: vnodes must be positive")
	// ErrNodeExists is returned by Add when the node ID is already on the ring.
	ErrNodeExists = errors.New("ring: node already exists")
	// ErrNodeNotFound is returned by Remove for an unknown node ID.
	ErrNodeNotFound = errors.New("ring: node not found")
)
