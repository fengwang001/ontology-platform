// Package api is the public facade over serial number arithmetic:
// construction, the four-state comparison, monotonic unwrapping, and a
// built-in self-check. It depends only on unwrap (which depends on sar).
package api

import (
	"ontology/sar"
	"ontology/unwrap"
)

// Rel is the four-state relative order, re-exported for callers.
type Rel = sar.Rel

// Re-exported relations.
const (
	Equal        = sar.Equal
	Less         = sar.Less
	Incomparable = sar.Incomparable
	Greater      = sar.Greater
)

// Decidable, mutually distinct sentinel errors.
var (
	ErrWidth        = sar.ErrWidth
	ErrOutOfRange   = sar.ErrOutOfRange
	ErrIncomparable = sar.ErrIncomparable
	ErrGreater      = sar.ErrGreater
)

// API is a goroutine-safe serial number space of a fixed width.
type API struct {
	u *unwrap.Unwrapper
}

// New creates a width-N space; N must satisfy 1 <= N <= 63.
func New(N int) (*API, error) {
	u, err := unwrap.New(N)
	if err != nil {
		return nil, err
	}
	return &API{u: u}, nil
}

// Cmp is the pure four-state comparison of two residues. Safe for
// unlimited concurrent use.
func (x *API) Cmp(a, b uint64) Rel { return x.u.Cmp(a, b) }

// Feed expands one residue into its monotonic absolute int64 value.
// Rejected feeds (out of range, half-circle, rewind) leave Last untouched.
func (x *API) Feed(s uint64) (absolute int64, err error) {
	return x.u.Feed(s)
}

// Last reports the current absolute last and whether any value was accepted.
func (x *API) Last() (int64, bool) { return x.u.Last() }

// Width reports N.
func (x *API) Width() int { return x.u.Width() }

// SelfCheck verifies the four invariants on built-in streams; it mutates
// no state of x and is safe to call concurrently with readers.
func (x *API) SelfCheck() error { return x.u.SelfCheck() }
