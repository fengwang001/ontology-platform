package ontology

import (
	"math"
	"math/big"
	"strconv"
)

// CompareKeys compares rows according to the same value order used by Encode.
// Directions only reverse non-null values; nil and NaN are always smallest.
func CompareKeys(a, b []Key, directions []Direction) int {
	length := len(a)
	if len(b) < length {
		length = len(b)
	}
	for index := 0; index < length; index++ {
		result := compareOne(a[index], b[index])
		if len(directions) > index && directions[index] == Desc {
			result = reverseNonNull(a[index], b[index], result)
		}
		if result != 0 {
			return result
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}

func compareOne(a, b Key) int {
	aNull := isNullKey(a)
	bNull := isNullKey(b)
	if aNull || bNull {
		return compareBools(aNull, bNull)
	}
	aNumber, aIsNumber := numberAsRat(a)
	bNumber, bIsNumber := numberAsRat(b)
	if aIsNumber && bIsNumber {
		if result := aNumber.Cmp(bNumber); result != 0 {
			return result
		}
		// Equal int64 and float64 values need a deterministic, retained tie break.
		return compareBools(isFloat(a), isFloat(b))
	}
	if aIsNumber != bIsNumber {
		return compareBools(bIsNumber, aIsNumber)
	}
	return compareBytes([]byte(a.(string)), []byte(b.(string)))
}

func isNullKey(key Key) bool {
	if key == nil {
		return true
	}
	value, ok := key.(float64)
	return ok && math.IsNaN(value)
}

func numberAsRat(key Key) (*big.Rat, bool) {
	switch value := key.(type) {
	case int64:
		return big.NewRat(value, 1), true
	case float64:
		// NaN is handled as null before this function is called.
		result, ok := new(big.Rat).SetString(strconv.FormatFloat(value, 'g', -1, 64))
		return result, ok
	default:
		return nil, false
	}
}

func isFloat(key Key) bool {
	_, ok := key.(float64)
	return ok
}

func reverseNonNull(a, b Key, result int) int {
	if isNullKey(a) || isNullKey(b) {
		return result
	}
	return -result
}

func compareBools(a, b bool) int {
	if a == b {
		return 0
	}
	if a {
		return -1
	}
	return 1
}

func compareBytes(a, b []byte) int {
	size := len(a)
	if len(b) < size {
		size = len(b)
	}
	for index := 0; index < size; index++ {
		if a[index] < b[index] {
			return -1
		}
		if a[index] > b[index] {
			return 1
		}
	}
	return compareBools(len(b) >= len(a), len(a) >= len(b))
}
