// Package hll implements an in-process HyperLogLog cardinality estimator.
//
// The estimator consumes precomputed 64-bit hash values (see Estimator.Add)
// and estimates the number of distinct hashes seen so far (see Estimator.Count).
// Multiple estimators configured with the same precision p can be merged
// register-wise (see Merge).
//
// Deletion is deliberately unsupported: HyperLogLog registers only remember
// the maximum rank (rho) ever observed per register. A Remove operation cannot
// know whether the value being deleted was the unique contributor of the
// current maximum, so deleting it would corrupt the estimate. If deletion is
// required, use a different sketch (for example a count-min or a
// linear-probabilistic counter backed by counts).
package hll

import (
	"errors"
	"math/bits"
	"strconv"
)

// MinPrecision and MaxPrecision bound the configurable precision p.
// With precision p there are 2^p registers; p must be in [4, 16].
const (
	MinPrecision = 4
	MaxPrecision = 16
)

// ErrNilEstimator is returned when Merge receives a nil estimator.
var ErrNilEstimator = errors.New("hll: cannot merge nil estimator")

// PrecisionMismatchError is returned by Merge when the two estimators were
// constructed with different precision values. Both values are exposed so
// callers can decide programmatically.
type PrecisionMismatchError struct {
	PA int
	PB int
}

func (e PrecisionMismatchError) Error() string {
	return "hll: cannot merge estimators with different precision: " +
		"p1=" + strconv.Itoa(e.PA) + " p2=" + strconv.Itoa(e.PB)
}

// Estimator is a HyperLogLog sketch. It is safe for concurrent use only when
// callers synchronize externally.
type Estimator struct {
	p         int
	m         uint64
	registers []uint8
}

// New returns an Estimator with 2^p registers. It returns an error when p is
// outside [MinPrecision, MaxPrecision].
func New(p int) (*Estimator, error) {
	if p < MinPrecision || p > MaxPrecision {
		return nil, errors.New("hll: precision p must be in [" +
			strconv.Itoa(MinPrecision) + ", " + strconv.Itoa(MaxPrecision) +
			"], got " + strconv.Itoa(p))
	}
	m := 1 << uint(p)
	return &Estimator{
		p:         p,
		m:         uint64(m),
		registers: make([]uint8, m),
	}, nil
}

// Precision returns the precision p the estimator was constructed with.
func (e *Estimator) Precision() int {
	return e.p
}

// rho computes the rank of a hash given precision p: the 1-based position of
// the leftmost 1 bit within the high (64-p) bits, i.e. leading zeros plus
// one. When all high bits are zero the rank is 64-p (the maximum possible).
func rho(p int, w uint64) uint8 {
	if w == 0 {
		return uint8(64 - p)
	}
	return uint8(bits.LeadingZeros64(w) + 1 - p)
}

// Add observes one precomputed 64-bit hash. The low p bits select a register;
// the remaining high bits determine the rank rho, which replaces the register
// value only when it is larger. Adding the same hash repeatedly is idempotent.
func (e *Estimator) Add(hash uint64) {
	idx := hash & (e.m - 1)
	r := rho(e.p, hash>>uint(e.p))
	if cur := e.registers[idx]; r > cur {
		e.registers[idx] = r
	}
}

// InspectRegisters returns a defensive copy of every register value. Mutating
// the returned slice does not affect the estimator. It is intended for tests
// and verification of register semantics.
func (e *Estimator) InspectRegisters() []uint8 {
	out := make([]uint8, len(e.registers))
	copy(out, e.registers)
	return out
}
