package hll

import "math"

// Estimate returns the estimated number of distinct hashes seen so far.
//
// Exactness contract: for 0..sparseLimit-1 distinct hashes the answer is the
// exact distinct count (0 elements yield 0), because the sparse mode tracks
// every hash. Beyond that threshold the standard HyperLogLog estimate is
// used, with linear-counting correction when registers remain empty.
func (e *Estimator) Estimate() uint64 {
	if !e.dense {
		return uint64(e.distinct)
	}

	m := float64(e.m)
	var sum float64
	zeros := 0
	for _, r := range e.regs {
		sum += 1.0 / float64(uint64(1)<<r)
		if r == 0 {
			zeros++
		}
	}

	alpha := alphaM(e.m)
	raw := alpha * m * m / sum

	// Small-range correction: with empty registers left, linear counting on
	// the fraction of untouched registers is unbiased and far tighter.
	if raw <= 2.5*m && zeros != 0 {
		return uint64(math.Round(-m * math.Log(float64(zeros)/m)))
	}
	// No 32-bit hash range correction is needed: inputs are full 64-bit
	// hashes, so the large-range saturation threshold is astronomically far.
	return uint64(math.Round(raw))
}

// alphaM returns the HLL bias-correction constant alpha as a function of the
// register count, using the small-m special constants from the original
// Flajolet et al. analysis.
func alphaM(m uint64) float64 {
	switch m {
	case 16:
		return 0.673
	case 32:
		return 0.697
	case 64:
		return 0.709
	default:
		return 0.7213 / (1 + 1.079/float64(m))
	}
}
