package ontology

import "math"

// CompareRows compares two rows key by key and returns -1, 0 or +1.
// It is the reference ordering that the byte encoding preserves:
//
//   - nil (and float64 NaN) sorts before any other value;
//   - numbers sort before strings;
//   - int64 and float64 compare by exact numeric value, with ties
//     broken by type (int64 before float64);
//   - strings compare byte-wise;
//   - a prefix row sorts before a longer row.
func CompareRows(a, b []any) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := Compare(a[i], b[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

// Compare compares two single keys; see CompareRows for the rules.
func Compare(a, b any) int {
	an, bn := isNilKey(a), isNilKey(b)
	switch {
	case an && bn:
		return 0
	case an:
		return -1
	case bn:
		return 1
	}
	ar, br := keyRank(a), keyRank(b)
	if ar != br {
		if ar < br {
			return -1
		}
		return 1
	}
	switch av := a.(type) {
	case int64:
		switch bv := b.(type) {
		case int64:
			return cmpOrdered(av, bv)
		case float64:
			return compareIntFloat(av, bv)
		}
	case float64:
		switch bv := b.(type) {
		case int64:
			return -compareIntFloat(bv, av)
		case float64:
			return cmpOrdered(av, bv)
		}
	case string:
		return cmpOrdered(av, b.(string))
	}
	panic("ontology: unsupported key type")
}

func isNilKey(k any) bool {
	if k == nil {
		return true
	}
	f, ok := k.(float64)
	return ok && math.IsNaN(f)
}

// keyRank ranks non-nil keys: all numbers share rank 0 so that int64
// and float64 compare numerically; strings have rank 1.
func keyRank(k any) int {
	if _, ok := k.(string); ok {
		return 1
	}
	return 0
}

func cmpOrdered[T int64 | float64 | string](a, b T) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// compareIntFloat compares an int64 with a non-NaN float64 exactly,
// without rounding the int64 to float64 precision.
func compareIntFloat(i int64, f float64) int {
	const two63 = float64(uint64(1) << 63)
	switch {
	case f >= two63:
		return -1
	case f < -two63:
		return 1
	}
	fi := int64(f) // truncates toward zero; |f| < 2^63 so no overflow
	if i != fi {
		return cmpOrdered(i, fi)
	}
	frac := f - float64(fi) // exact: both are integers in range
	switch {
	case frac > 0:
		return -1
	case frac < 0:
		return 1
	}
	// Numerically equal: int64 sorts before float64.
	return -1
}
