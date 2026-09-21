package sparsevec

import "math"

// neumaier accumulates x into a compensated sum (Neumaier's improved Kahan).
// The result is independent of rounding drift for same-order summation and
// keeps small terms accurate next to huge ones.
type neumaier struct {
	sum float64
	c   float64
}

func (n *neumaier) add(x float64) {
	t := n.sum + x
	if math.Abs(n.sum) >= math.Abs(x) {
		n.c += (n.sum - t) + x
	} else {
		n.c += (x - t) + n.sum
	}
	n.sum = t
}

func (n *neumaier) total() float64 { return n.sum + n.c }

// mergeStats carries the observable counters produced by one merge pass.
type mergeStats struct {
	steps int
}

// merge walks a and b once with two pointers, calling onMatch for every
// shared index. It never expands either vector into dense form.
func merge(a, b Vector, onMatch func(av, bv float64)) mergeStats {
	var st mergeStats
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		st.steps++
		switch {
		case a[i].Index < b[j].Index:
			i++
		case a[i].Index > b[j].Index:
			j++
		default:
			onMatch(a[i].Value, b[j].Value)
			i++
			j++
		}
	}
	return st
}

// Dot returns the dot product of a and b plus merge statistics.
// The merge order depends only on indices, so the result is bit-identical
// regardless of how elements were ordered before sorting.
func Dot(a, b Vector) (float64, Stats, error) {
	st, err := check(a, b)
	if err != nil {
		return 0, Stats{}, err
	}
	var acc neumaier
	ms := merge(a, b, func(av, bv float64) {
		acc.add(av * bv)
	})
	st.Steps = ms.steps
	dot := acc.total()
	if math.IsNaN(dot) || math.IsInf(dot, 0) {
		return 0, Stats{}, nonFiniteErr("dot product", dot)
	}
	return dot, st, nil
}

// check validates both operands and seeds Stats with zero counts.
func check(a, b Vector) (Stats, error) {
	za, err := validate(0, a)
	if err != nil {
		return Stats{}, err
	}
	zb, err := validate(1, b)
	if err != nil {
		return Stats{}, err
	}
	return Stats{ZerosA: za, ZerosB: zb}, nil
}
