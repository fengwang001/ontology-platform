package quantile

import (
	"math"
	"strconv"
)

// AddWeighted records value with weight given as a float64. The weight must be
// a finite positive integer: zero, negative, non-integer, NaN or infinite
// weights are rejected with ErrInvalidWeight before value is inspected.
//
// It exists so that non-integer weights can be rejected with a decidable
// error at insertion time; Add(v, uint64(w)) is equivalent when w is valid.
func (s *Sketch) AddWeighted(value, weight float64) error {
	maxInt := strconv.FormatUint(^uint64(0), 10)
	maxUint, _ := strconv.ParseFloat(maxInt, 64)
	if weight != weight || weight <= 0 || math.IsInf(weight, 0) ||
		weight != math.Trunc(weight) || weight >= maxUint {
		return ErrInvalidWeight
	}
	return s.Add(value, uint64(weight))
}
