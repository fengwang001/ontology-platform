package battery

import "math/bits"

func lookupPermille(table LimitTable, value int64) int {
	lo, hi := 0, len(table.Boundaries)
	for lo < hi {
		mid := lo + (hi-lo)/2
		if value < table.Boundaries[mid] {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	if lo == 0 {
		lo = 1
	}
	return table.Permilles[lo-1]
}

func scaleCurrent(rated int64, permille int) int64 {
	if permille == 1000 {
		return rated
	}
	hi, lo := bits.Mul64(uint64(rated), uint64(permille))
	quotient, _ := bits.Div64(hi, lo, 1000)
	return int64(quotient)
}

func minInt64(values ...int64) int64 {
	result := values[0]
	for _, value := range values[1:] {
		if value < result {
			result = value
		}
	}
	return result
}

func absoluteDifference(left, right int64) int64 {
	if left >= right {
		return left - right
	}
	return right - left
}
