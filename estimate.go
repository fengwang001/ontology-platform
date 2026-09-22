package hll

import "math"

// alphaM returns the standard HLL bias-correction constant for m registers.
func alphaM(m int) float64 {
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

// Estimate returns the estimated number of distinct hashes observed.
//
// An empty sketch returns exactly 0. When registers are sparse the
// small-range (linear counting) estimator is used: m * ln(m / V), where V is
// the number of zero registers. Otherwise the standard harmonic-mean
// HyperLogLog estimator is used, with the large-range correction when the raw
// estimate exceeds the threshold at which hash-space saturation matters.
func (e *Estimator) Estimate() uint64 {
	m := len(e.registers)
	zeros := 0
	sum := 0.0
	for _, r := range e.registers {
		if r == 0 {
			zeros++
		}
		sum += 1.0 / float64(uint64(1)<<uint(r))
	}
	if zeros == m {
		return 0
	}

	alpha := alphaM(m)
	raw := alpha * float64(m) * float64(m) / sum

	// Small-range correction: linear counting.
	if raw <= 2.5*float64(m) {
		est := float64(m) * math.Log(float64(m)/float64(zeros))
		return uint64(math.Round(est))
	}

	// Large-range correction for 64-bit hashes.
	const twoTo64 = float64(1 << 63) * 2
	if raw > twoTo64/30 {
		est := -twoTo64 * math.Log(1-raw/twoTo64)
		return uint64(math.Round(est))
	}
	return uint64(math.Round(raw))
}
