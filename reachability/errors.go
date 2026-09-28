// Package reachability incrementally maintains all reachable ordered node
// pairs of a directed multigraph as edges are added and removed.
package reachability

import "errors"

// Distinct error values returned by the component. Callers can use errors.Is
// to tell rejection reasons apart.
var (
	// ErrInvalidArgument is returned for arguments that are structurally
	// invalid (for example a negative edge limit).
	ErrInvalidArgument = errors.New("reachability: invalid argument")

	// ErrEmptyNode is returned when a node name is the empty string.
	ErrEmptyNode = errors.New("reachability: node name must not be empty")

	// ErrEdgeNotFound is returned when an edge is removed whose multiplicity
	// is already zero.
	ErrEdgeNotFound = errors.New("reachability: edge does not exist")

	// ErrTooManyEdges is returned when adding a new distinct edge would exceed
	// the configured edge limit.
	ErrTooManyEdges = errors.New("reachability: edge limit exceeded")
)
