package alloc

import (
	"math"
	"math/bits"
	"sort"
)

// Allocate distributes amount across parties proportionally to weights
// and returns a slice with the same length as weights.
//
// Each party first receives floor(|amount|*weights[i]/sum(weights)) in
// magnitude; the remaining units are assigned one by one to the largest
// remainders (largest remainder method). Ties are broken by the smaller
// index, so the result is deterministic. For negative amounts the same
// computation is done on the magnitude and the sign is applied at the
// end, which makes the top-up direction negative as well.
//
// Invariants: sum(result) == amount exactly; a zero weight receives 0;
// weights[i] > weights[j] implies result[i] >= result[j] in magnitude.
func Allocate(amount int64, weights []int64) ([]int64, error) {
	if len(weights) == 0 {
		return nil, ErrEmptyWeights
	}
	var total uint64
	for _, w := range weights {
		if w < 0 {
			return nil, ErrNegativeWeight
		}
		sum, carry := bits.Add64(total, uint64(w), 0)
		if carry != 0 || sum > math.MaxInt64 {
			return nil, ErrOverflow
		}
		total = sum
	}
	if total == 0 {
		return nil, ErrZeroTotalWeight
	}

	neg := amount < 0
	mag := uint64(amount)
	if neg {
		mag = 0 - mag // |amount|, correct even for math.MinInt64
	}

	quot := make([]uint64, len(weights))
	rem := make([]uint64, len(weights))
	var sumQuot uint64
	for i, w := range weights {
		if w == 0 {
			continue
		}
		hi, lo := bits.Mul64(mag, uint64(w))
		// The signed product amount*w must fit in int64. In magnitude
		// terms that means lo <= MaxInt64, or lo == 2^63 exactly when
		// amount is negative (the product is then math.MinInt64).
		limit := uint64(math.MaxInt64)
		if neg {
			limit++
		}
		if hi != 0 || lo > limit {
			return nil, ErrOverflow
		}
		quot[i] = lo / total
		rem[i] = lo % total
		sumQuot += quot[i]
	}
	// residual < number of positive weights, so the loop below is safe.
	residual := mag - sumQuot

	order := make([]int, 0, len(weights))
	for i, w := range weights {
		if w > 0 {
			order = append(order, i)
		}
	}
	sort.Slice(order, func(a, b int) bool {
		if rem[order[a]] != rem[order[b]] {
			return rem[order[a]] > rem[order[b]]
		}
		return order[a] < order[b]
	})
	for k := uint64(0); k < residual; k++ {
		quot[order[k]]++
	}

	out := make([]int64, len(weights))
	for i := range out {
		if neg {
			out[i] = int64(0 - quot[i])
		} else {
			out[i] = int64(quot[i])
		}
	}
	return out, nil
}
