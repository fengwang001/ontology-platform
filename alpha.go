package hll

// alphaM returns the HyperLogLog bias-correction constant for m registers:
//
//	alpha(16) = 0.673
//	alpha(32) = 0.697
//	alpha(64) = 0.709
//	alpha(m)  = 0.7213 / (1 + 1.079/m)  for m >= 128
//
// m is always a power of two between 2^4 and 2^16 because p is validated by
// New.
func alphaM(m int) float64 {
	switch m {
	case 16:
		return 0.673
	case 32:
		return 0.697
	case 64:
		return 0.709
	default:
		fm := float64(m)
		return 0.7213 / (1 + 1.079/fm)
	}
}
