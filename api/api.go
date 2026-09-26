// Package api exposes the incremental Delaunay triangulation service.
package api

import (
	"ontology/geo"
	"ontology/tri"
)

// Point is a 2D integer coordinate.
type Point = geo.Point

var (
	ErrDuplicate = tri.ErrDuplicate
	ErrCollinear = tri.ErrCollinear
	ErrRange     = tri.ErrRange
)

// API is the service handle. Safe for concurrent use.
type API struct{ t *tri.T }

// New creates an empty triangulation service.
func New() (*API, error) { return &API{t: tri.New()}, nil }

// Insert adds one point; failures are distinguishable and leave no trace.
func (a *API) Insert(x, y int) error { return a.t.Insert(x, y) }

// Triangles returns all current triangles, each counter-clockwise.
func (a *API) Triangles() [][3]Point { return a.t.Triangles() }

// Hull returns the convex hull vertices counter-clockwise.
func (a *API) Hull() []Point { return a.t.Hull() }

// SelfCheck verifies the invariants on a built-in point sequence.
func (a *API) SelfCheck() error { return tri.SelfCheck() }
