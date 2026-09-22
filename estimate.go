package ontology

import "math"

// alpha is the bias-correction constant for m registers.
func alpha(m float64) float64 {
	switch m {
	case 16:
		return 0.673
	case 32:
		return 0.697
	case 64:
		return 0.709
	default:
		return 0.7213 / (1 + 1.079/m)
	}
}

// Estimate returns the estimated number of distinct hashes added so far.
//
// The raw HyperLogLog estimate is alpha(m) * m^2 / sum(2^-reg). In the
// small-cardinality range (raw estimate <= 5/2 * m) it is replaced by
// linear counting, m * ln(m/zeros), rounded to the nearest integer so
// tiny cardinalities come back exact.
func (h *HLL) Estimate() uint64 {
	m := float64(len(h.reg))
	var sum, zeros float64
	for _, r := range h.reg {
		sum += math.Ldexp(1, -int(r))
		if r == 0 {
			zeros++
		}
	}
	raw := alpha(m) * m * m / sum
	if raw <= 2.5*m && zeros > 0 {
		return uint64(math.Round(m * math.Log(m/zeros)))
	}
	return uint64(math.Round(raw))
}
