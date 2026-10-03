package quantile

import (
	"math"
	"math/bits"

	"ontology/schema"
)

// Result 是分位数估计结果。
type Result struct {
	Value     int64
	Saturated bool
}

// div128 计算 (hi<<64+lo)/d，要求 hi < d（商不超 64 位）。
func div128(hi, lo, d uint64) uint64 {
	if hi == 0 {
		return lo / d
	}
	const b = uint64(1) << 32
	u2, u1 := hi/b, hi%b
	l1, l0 := lo/b, lo%b
	q1, r1 := bits.Div64(u2, u1*b+l1, d)
	q0, _ := bits.Div64(r1/b, (r1%b)*b+l0, d)
	return q1*b + q0
}

// Quantile 按千分位 q 估计分位数。
func Quantile(h schema.Hist, q int) (Result, error) {
	if q < 0 || q > 1000 || !h.Valid() {
		return Result{}, schema.ErrInvalidArgument
	}
	var n uint64
	for _, c := range h.Counts {
		n += c
	}
	if n == 0 {
		return Result{}, schema.ErrEmpty
	}
	hi, lo := bits.Mul64(uint64(q), n)
	loC, carry := bits.Add64(lo, 999, 0)
	r := div128(hi+carry, loC, 1000)
	if r < 1 {
		r = 1
	}
	var cum, prev uint64
	var i int
	for i = range h.Counts {
		cum += h.Counts[i]
		if cum >= r {
			break
		}
		prev = cum
	}
	if i == len(h.Bounds) {
		return Result{Value: h.Bounds[len(h.Bounds)-1], Saturated: true}, nil
	}
	var loBound int64
	if i > 0 {
		loBound = h.Bounds[i-1]
	}
	hiBound := h.Bounds[i]
	c := h.Counts[i]
	w := r - prev
	phi, plo := bits.Mul64(uint64(hiBound-loBound), w)
	frac := div128(phi, plo, c)
	if frac > math.MaxInt64 || loBound > math.MaxInt64-int64(frac) {
		return Result{}, schema.ErrOverflow
	}
	return Result{Value: loBound + int64(frac)}, nil
}
