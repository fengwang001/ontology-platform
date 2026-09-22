package ontology

import (
	"errors"
	"fmt"
	"math/bits"
)

// MinP and MaxP bound the precision parameter p. The estimator keeps
// 1<<p registers, so p trades memory for accuracy.
const (
	MinP = 4
	MaxP = 16
)

// ErrInvalidP is returned by New when p is outside [MinP, MaxP].
var ErrInvalidP = errors.New("ontology: precision p out of range")

// HLL is a HyperLogLog cardinality estimator. The zero value is not
// usable; construct one with New.
type HLL struct {
	p   uint8
	reg []uint8
}

// New creates an estimator with precision p (MinP <= p <= MaxP) and
// 1<<p zeroed registers.
func New(p uint8) (*HLL, error) {
	if p < MinP || p > MaxP {
		return nil, fmt.Errorf("%w: got %d, want %d..%d", ErrInvalidP, p, MinP, MaxP)
	}
	return &HLL{p: p, reg: make([]uint8, 1<<p)}, nil
}

// P returns the precision the estimator was built with.
func (h *HLL) P() uint8 { return h.p }

// index returns the register selected by the low p bits of hash.
func (h *HLL) index(hash uint64) uint32 {
	return uint32(hash & (uint64(len(h.reg)) - 1))
}

// rho returns the number of leading zeros of the bits above the low p
// index bits, plus one. A hash whose remaining bits are all zero yields
// the maximum rho of 64-p+1.
func (h *HLL) rho(hash uint64) uint8 {
	return uint8(bits.LeadingZeros64(hash>>h.p)-int(h.p)) + 1
}

// Add records one 64-bit hash. It is idempotent: re-adding the same
// hash never changes the estimate.
func (h *HLL) Add(hash uint64) {
	i := h.index(hash)
	if r := h.rho(hash); r > h.reg[i] {
		h.reg[i] = r
	}
}

// InspectRegisters returns a copy of the register snapshot. It exists
// for testing and auditing; mutating the result does not affect the
// estimator.
func (h *HLL) InspectRegisters() []uint8 {
	out := make([]uint8, len(h.reg))
	copy(out, h.reg)
	return out
}
