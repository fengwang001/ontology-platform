package agg

import "math"

func bitExtremes(vals []float64) (lo, hi float64) {
	lo, hi = vals[0], vals[0]
	for _, v := range vals[1:] {
		if math.Float64bits(v) < math.Float64bits(lo) {
			lo = v
		}
		if math.Float64bits(v) > math.Float64bits(hi) {
			hi = v
		}
	}
	return lo, hi
}
