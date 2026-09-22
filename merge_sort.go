package ontology

import "math"

// sortKey is the reduced per-row information needed inside one partition.
type sortKey struct {
	value float64
	id    string
}

// lessKey reports whether a must be ordered before b. Ties on value fall
// back to ascending row ID so ties have a deterministic order. Direction
// affects only the value axis: IDs inside a tie always ascend.
func lessKey(a, b sortKey, dir Direction, cmp *int) bool {
	*cmp++
	av, bv := a.value, b.value
	if dir == Descending {
		av, bv = -av, -bv
	}
	if av != bv {
		return av < bv
	}
	return a.id < b.id
}

// mergeSortKeys sorts keys using a stable top-down merge sort and returns the
// number of comparisons performed. The comparison count is bounded by
// n*ceil(log2(n)) ... n*ceil(log2(n+1)); the test budget is more generous.
func mergeSortKeys(keys []sortKey, dir Direction) int {
	cmp := 0
	if len(keys) < 2 {
		return cmp
	}
	buf := make([]sortKey, len(keys))
	var sort func(lo, hi int)
	sort = func(lo, hi int) {
		if hi-lo < 2 {
			return
		}
		mid := lo + (hi-lo)/2
		sort(lo, mid)
		sort(mid, hi)
		merge(keys, buf, lo, mid, hi, dir, &cmp)
	}
	sort(0, len(keys))
	return cmp
}

func merge(dst, buf []sortKey, lo, mid, hi int, dir Direction, cmp *int) {
	i, j, k := lo, mid, lo
	for i < mid && j < hi {
		if lessKey(dst[i], dst[j], dir, cmp) {
			buf[k] = dst[i]
			i++
		} else {
			buf[k] = dst[j]
			j++
		}
		k++
	}
	for i < mid {
		buf[k] = dst[i]
		i++
		k++
	}
	for j < hi {
		buf[k] = dst[j]
		j++
		k++
	}
	copy(dst[lo:hi], buf[lo:hi])
}

// compareBudget is the test-side ceiling 10*n*ceil(log2(n+1)).
func compareBudget(n int) int {
	return 10 * n * int(math.Ceil(math.Log2(float64(n+1))))
}
