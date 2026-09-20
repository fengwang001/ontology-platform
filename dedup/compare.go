package dedup

import (
	"cmp"
	"math"
	"sort"
)

// kindRank defines the cross-type ascending order of key parts:
// missing < nil < number < string < bool < NaN < other.
func kindRank(k partKind) int {
	switch k {
	case partMissing:
		return 0
	case partNil:
		return 1
	case partNumber:
		return 2
	case partString:
		return 3
	case partBool:
		return 4
	case partNaN:
		return 5
	default:
		return 6
	}
}

// sortEntries orders entries by their dedup key parts, column by column.
// The order is fully determined by the key, never by arrival order.
func sortEntries(entries []*entry) {
	sort.SliceStable(entries, func(i, j int) bool {
		return lessParts(entries[i].parts, entries[j].parts)
	})
}

// lessParts compares two keys column by column.
func lessParts(a, b []keyPart) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := comparePart(a[i], b[i]); c != 0 {
			return c < 0
		}
	}
	return len(a) < len(b)
}

// comparePart returns -1, 0 or 1 for a single column position.
func comparePart(a, b keyPart) int {
	if a.kind != b.kind {
		return cmp.Compare(kindRank(a.kind), kindRank(b.kind))
	}
	switch a.kind {
	case partNumber:
		return compareNumber(a, b)
	case partString:
		return cmp.Compare(a.str, b.str)
	case partBool:
		return cmp.Compare(boolRank(a.b), boolRank(b.b))
	case partNaN, partOther:
		return cmp.Compare(a.seq, b.seq)
	default:
		return 0
	}
}

// compareNumber compares numerically across int64 and float64.
// Ties broken deterministically; grouping already decided equality.
func compareNumber(a, b keyPart) int {
	if a.isInt && b.isInt {
		return cmp.Compare(a.i64, b.i64)
	}
	fa, fb := a.f64, b.f64
	if a.isInt {
		fa = float64(a.i64)
	}
	if b.isInt {
		fb = float64(b.i64)
	}
	if c := cmp.Compare(fa, fb); c != 0 {
		return c
	}
	// Equal as float64 but distinct keys (precision collapse):
	// order integral parts before fractional ones, then by exact value.
	if a.isInt != b.isInt {
		if a.isInt {
			return -1
		}
		return 1
	}
	return cmp.Compare(math.Float64bits(a.f64), math.Float64bits(b.f64))
}

func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}
