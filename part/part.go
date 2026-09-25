// Package part models one range partition and the two pruning judgments.
// It depends on no other package in the module.
package part

import "errors"

// ErrInvalidPartition is the decidable error for an illegal partition:
// an empty p range (lo >= hi) or inverted v statistics (min_v > max_v).
var ErrInvalidPartition = errors.New("part: invalid partition: lo >= hi or min_v > max_v")

// Part is one range partition: p in [Lo, Hi), with true v min/max [MinV, MaxV].
type Part struct {
	id         string
	lo, hi     int64
	minV, maxV int64
}

// New validates before constructing; a rejected call returns the zero value,
// so failure leaves nothing behind.
func New(id string, lo, hi, minV, maxV int64) (Part, error) {
	if lo >= hi || minV > maxV {
		return Part{}, ErrInvalidPartition
	}
	return Part{id: id, lo: lo, hi: hi, minV: minV, maxV: maxV}, nil
}

func (p Part) ID() string  { return p.id }
func (p Part) Lo() int64   { return p.lo }
func (p Part) Hi() int64   { return p.hi }
func (p Part) MinV() int64 { return p.minV }
func (p Part) MaxV() int64 { return p.maxV }

// StaticPruned reports whether the p box [lo, hi) is disjoint from the
// p predicate [plo, phi): hi <= plo or lo >= phi (half-open, equality prunes).
func (p Part) StaticPruned(plo, phi int64) bool {
	return p.hi <= plo || p.lo >= phi
}

// DynamicPruned reports whether the closed v box [minV, maxV] is disjoint from
// the half-open v predicate [vlo, vhi): maxV < vlo or minV >= vhi.
func (p Part) DynamicPruned(vlo, vhi int64) bool {
	return p.maxV < vlo || p.minV >= vhi
}
