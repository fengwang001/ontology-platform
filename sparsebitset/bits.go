package sparsebitset

import "math/bits"

func trailingZeros64(x uint64) int {
	return bits.TrailingZeros64(x)
}

func popcount64(x uint64) int {
	return bits.OnesCount64(x)
}
