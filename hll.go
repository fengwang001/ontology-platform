// Package hll implements a HyperLogLog cardinality estimator for streams of
// caller-hashed 64-bit values.
//
// The estimator keeps all state in process memory and depends only on the Go
// standard library. It answers one question: approximately how many distinct
// uint64 hash values have been observed? Multiple estimators with the same
// precision can be merged.
//
// # What HyperLogLog does NOT support
//
// HyperLogLog is a register of per-bucket maxima: Add compares a value against
// the current register and only ever writes a strictly larger value. There is
// no record of which element produced the maximum, and no reverse operation.
// Therefore deletion (a Remove operation) is fundamentally unsupported:
// removing one element cannot tell the estimator what the second-largest value
// was, and "subtracting" the hash is meaningless. To support deletion you need
// a different data structure (for example a sketch retaining per-item state),
// not HyperLogLog. Repeatedly adding the same hash is, by contrast,
// inherently idempotent.
package hll

import (
	"errors"
	"math"
	"math/bits"
)

// MinPrecision and MaxPrecision bound the configurable number of index bits p.
// The estimator therefore uses 2^p registers, from 16 (p=4) to 65536 (p=16).
const (
	MinPrecision = 4
	MaxPrecision = 16
)

// ErrPrecision is returned by New when p is outside [MinPrecision, MaxPrecision].
var ErrPrecision = errors.New("hll: precision p must be between 4 and 16 inclusive")

// Estimator is an in-memory HyperLogLog sketch. It is intended for serial
// (single-goroutine) use; concurrent callers must provide their own
// synchronization.
type Estimator struct {
	p         uint8
	registers []uint8 // length 2^p; each entry stores rho(w), in [1, 64-p+1]
}

// New returns an empty estimator using p index bits (2^p registers).
// It returns ErrPrecision when p is outside [4, 16].
func New(p int) (*Estimator, error) {
	if p < MinPrecision || p > MaxPrecision {
		return nil, ErrPrecision
	}
	return &Estimator{
		p:         uint8(p),
		registers: make([]uint8, 1<<uint(p)),
	}, nil
}

// Precision returns the estimator's index-bit precision p.
func (e *Estimator) Precision() int {
	return int(e.p)
}

// rho computes the rank of the high (64-p) bits of a hash: 1 plus the number
// of leading zero bits of those remaining bits, read from the most significant
// end. When all remaining bits are zero the rank saturates at 64-p+1.
func rho(p uint8, h uint64) uint8 {
	// Keep the high 64-p bits at the most significant end by clearing the
	// p low index bits, so LeadingZeros64 counts zeros within the remaining
	// field rather than including stripped index bits.
	remaining := h &^ ((uint64(1) << p) - 1)
	if remaining == 0 {
		return uint8(64-p) + 1
	}
	return uint8(bits.LeadingZeros64(remaining)) + 1
}

// Add observes one 64-bit hash value. The low p bits select the register and
// the remaining high bits determine rho. The register is updated only when the
// new rank is strictly larger than its current value, so adding an already
// observed hash never changes the state.
func (e *Estimator) Add(h uint64) {
	idx := h & (uint64(len(e.registers)) - 1)
	r := rho(e.p, h)
	if r > e.registers[idx] {
		e.registers[idx] = r
	}
}

// rawStats returns the count of zero registers and the harmonic-mean sum of
// 2^-register over all registers.
func (e *Estimator) rawStats() (zeros int, sum float64) {
	for _, r := range e.registers {
		if r == 0 {
			zeros++
		}
		sum += 1.0 / float64(uint64(1)<<r)
	}
	return zeros, sum
}

// Estimate returns the estimated number of distinct hashes observed.
//
// It uses the standard HyperLogLog recipe:
//   - no register touched: exactly 0;
//   - small range (linear counting): when some registers are still zero and
//     the linear-counting value is below 2.5m, E = m * ln(m / V), where V is
//     the number of zero registers;
//   - otherwise the bias-corrected harmonic-mean HyperLogLog estimate
//     alpha(m) * m^2 / sum(2^-M[j]) (no large-range correction is needed for
//     64-bit hashes).
//
// The returned value is rounded to the nearest integer.
func (e *Estimator) Estimate() uint64 {
	zeros, sum := e.rawStats()
	if zeros == len(e.registers) {
		return 0
	}
	m := float64(len(e.registers))
	if zeros != 0 {
		linear := m * math.Log(m/float64(zeros))
		if linear < 2.5*m {
			return uint64(linear + 0.5)
		}
	}
	raw := alphaM(len(e.registers)) * m * m / sum
	return uint64(raw + 0.5)
}

// InspectRegisters returns a defensive copy of all register values, ordered by
// register index (hash low p bits). It is intended for tests and diagnostics;
// mutating the returned slice never affects the estimator.
func (e *Estimator) InspectRegisters() []uint8 {
	out := make([]uint8, len(e.registers))
	copy(out, e.registers)
	return out
}
