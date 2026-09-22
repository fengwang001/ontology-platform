// Package hll implements an in-process HyperLogLog cardinality estimator.
//
// Elements are presented to the estimator as precomputed 64-bit hash values
// (uint64). The estimator never hashes anything itself: the caller is
// responsible for producing a well-mixed 64-bit hash.
//
// See doc.go for the supported operation set and the explicit list of
// unsupported operations (notably deletion).
package hll

import "errors"

const (
	// MinPrecision and MaxPrecision bound the configurable register-count
	// exponent p: the estimator keeps m = 2^p registers.
	MinPrecision = 4
	MaxPrecision = 16

	// sparseLimit is the number of distinct hashes tracked exactly before
	// the estimator switches from exact counting to the HLL estimate. It is
	// strictly above the range for which callers require exact answers.
	sparseLimit = 256
)

// ErrInvalidPrecision is returned by New when p falls outside
// [MinPrecision, MaxPrecision].
var ErrInvalidPrecision = errors.New("hll: precision p must be between 4 and 16 inclusive")

// Estimator is a HyperLogLog sketch. The zero value is not usable; create one
// with New. An Estimator is not safe for concurrent use.
type Estimator struct {
	p        int
	m        uint64 // number of registers, 2^p
	regMask  uint64 // m-1, masks the low p register-index bits
	regs     []uint8
	seen     map[uint64]struct{} // exact distinct-hash set while sparse
	dense    bool                // true once sparse tracking has been dropped
	distinct int                 // exact distinct count retained while sparse
}

// New creates an Estimator using 2^p registers and validates p.
func New(p int) (*Estimator, error) {
	if p < MinPrecision || p > MaxPrecision {
		return nil, ErrInvalidPrecision
	}
	m := 1 << p
	return &Estimator{
		p:       p,
		m:       uint64(m),
		regMask: uint64(m - 1),
		regs:    make([]uint8, m),
		seen:    make(map[uint64]struct{}),
	}, nil
}

// Precision returns the configured register exponent p.
func (e *Estimator) Precision() int { return e.p }

// Add inserts one precomputed 64-bit hash. Registers are updated on every
// call per the strict HLL rule; in the sparse range the hash is additionally
// remembered exactly so Estimate stays exact for small cardinalities. Add is
// idempotent: adding the same hash again changes nothing.
func (e *Estimator) Add(hash uint64) {
	idx, r := e.split(hash)
	if r > e.regs[idx] {
		e.regs[idx] = r
	}
	if e.dense {
		return
	}
	if _, ok := e.seen[hash]; ok {
		return
	}
	e.seen[hash] = struct{}{}
	e.distinct++
	if e.distinct >= sparseLimit {
		e.seen = nil
		e.dense = true
	}
}
