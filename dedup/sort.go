package dedup

import "sort"

// sortGroups orders groups by dedup columns, column by column, breaking
// remaining ties (e.g. between two NaN groups) by a deterministic
// rendering of the kept row so output never depends on arrival order.
func sortGroups(gs []*group) {
	sort.Slice(gs, func(i, j int) bool {
		if c := compareSortVals(gs[i].vals, gs[j].vals); c != 0 {
			return c < 0
		}
		return gs[i].tie < gs[j].tie
	})
}

func compareSortVals(a, b []sortVal) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := compareSortVal(a[i], b[i]); c != 0 {
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

func compareSortVal(a, b sortVal) int {
	if a.rank != b.rank {
		return cmpInt(a.rank, b.rank)
	}
	switch a.rank {
	case rankBool:
		switch {
		case a.b == b.b:
			return 0
		case !a.b:
			return -1
		}
		return 1
	case rankNumber:
		return compareNum(a, b)
	case rankString:
		switch {
		case a.s < b.s:
			return -1
		case a.s > b.s:
			return 1
		}
	}
	return 0
}

// compareNum compares two numeric sortVals exactly across the
// int64/float64 boundary.
func compareNum(a, b sortVal) int {
	switch {
	case a.isInt && b.isInt:
		return cmpInt64(a.i, b.i)
	case a.isInt:
		return cmpIntFloat(a.i, b.f)
	case b.isInt:
		return -cmpIntFloat(b.i, a.f)
	default:
		return cmpFloat(a.f, b.f)
	}
}

// cmpIntFloat compares int64 i with float64 f without precision loss.
func cmpIntFloat(i int64, f float64) int {
	switch {
	case f >= 9223372036854775808.0:
		return -1
	case f < -9223372036854775808.0:
		return 1
	}
	fi := int64(f) // truncates toward zero; f is finite and in range
	switch {
	case i < fi:
		return -1
	case i > fi:
		return 1
	}
	switch {
	case f > float64(fi):
		return -1
	case f < float64(fi):
		return 1
	}
	return 0
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func cmpInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
