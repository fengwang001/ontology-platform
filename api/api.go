// Package api is the external entry point; it depends only on stats.
package api

import "ontology/stats"

// Op and View are re-exported so callers only import api.
type Op = stats.Op
type View = stats.View

// Operation kinds.
const OpAdd, OpRemove, OpMerge = stats.OpAdd, stats.OpRemove, stats.OpMerge

// Decidable sentinel errors.
var (
	ErrEmptyKey    = stats.ErrEmptyKey
	ErrValueAbsent = stats.ErrValueAbsent
	ErrMergeEmpty  = stats.ErrMergeEmpty
	ErrMergeSelf   = stats.ErrMergeSelf
)

// API is the online variance service.
type API struct{ s *stats.Store }

// New returns an empty service.
func New() *API { return &API{s: stats.NewStore()} }

// Apply applies one operation; rejected ops leave no trace.
func (a *API) Apply(op Op) error { return a.s.Apply(op) }

// View returns (n, mean, M2, variance, std) for a key.
func (a *API) View(key string) View { return a.s.View(key) }

// SelfCheck runs the built-in verification of the four invariants,
// including the eight-step sequence, decidable rejects, and O(1) lookups.
func (a *API) SelfCheck() error { return a.s.SelfCheck() }
