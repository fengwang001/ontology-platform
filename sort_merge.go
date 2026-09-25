package ontology

// compareValue orders finite or infinite values. +0.0 and -0.0 compare
// equal, so they form a tie. NaN rows are removed before sorting.
func compareValue(a, b float64) int {
	switch {
	case a == b:
		return 0
	case a < b:
		return -1
	default:
		return 1
	}
}

// itemLess is the total order within a partition: value first in the
// requested direction, then ID ascending regardless of direction.
func itemLess(a, b rankedItem, order Order) bool {
	c := compareValue(a.value, b.value)
	if c == 0 {
		return a.id < b.id
	}
	if order == Desc {
		return c > 0
	}
	return c < 0
}

// mergeSortItems sorts items with a stable top-down merge sort and
// returns the number of comparisons performed. The algorithm performs
// O(n log n) comparisons, with no map iteration or input-index use.
func mergeSortItems(items []rankedItem, order Order) int {
	comparisons := 0
	less := func(a, b rankedItem) bool {
		comparisons++
		return itemLess(a, b, order)
	}
	aux := make([]rankedItem, len(items))
	var sortRange func(lo, hi int)
	sortRange = func(lo, hi int) {
		if hi-lo < 2 {
			return
		}
		mid := lo + (hi-lo)/2
		sortRange(lo, mid)
		sortRange(mid, hi)
		i, j, k := lo, mid, lo
		for i < mid && j < hi {
			if less(items[i], items[j]) {
				aux[k] = items[i]
				i++
			} else {
				aux[k] = items[j]
				j++
			}
			k++
		}
		for i < mid {
			aux[k] = items[i]
			i++
			k++
		}
		for j < hi {
			aux[k] = items[j]
			j++
			k++
		}
		copy(items[lo:hi], aux[lo:hi])
	}
	if len(items) > 1 {
		sortRange(0, len(items))
	}
	return comparisons
}
