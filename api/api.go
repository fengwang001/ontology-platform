// Package api is the public entry point. It wraps a replica and re-exports the
// sentinel errors so callers can judge failures with errors.Is. It depends
// only on the replica package (which depends on delta).
package api

import (
	"ontology/delta"
	"ontology/replica"
)

// Sentinel errors: pairwise distinct, re-exported for errors.Is.
var (
	ErrGap             = replica.ErrGap
	ErrInvalidRange    = replica.ErrInvalidRange
	ErrNegativeVersion = replica.ErrNegativeVersion
	ErrEmptyKey        = delta.ErrEmptyKey
)

// API is the concurrency-safe facade over one in-memory replica.
type API struct {
	r *replica.Replica
}

// New returns an API backed by a replica at version 0 with an empty map.
func New() *API { return &API{r: replica.New()} }

// Apply feeds one delta, applying it in order, skipping duplicates, rejecting
// gaps and malformed deltas without leaving any trace.
func (a *API) Apply(d delta.Delta) error { return a.r.Apply(d) }

// State returns a copy of the current map; safe for concurrent readers.
func (a *API) State() map[string]int { return a.r.State() }

// Version returns the current (monotonic) version.
func (a *API) Version() int { return a.r.Version() }

// SelfCheck runs the built-in verification of the four invariants and the O(1)
// probe bound. It returns nil only if every check passes.
func (a *API) SelfCheck() error { return a.r.SelfCheck() }
