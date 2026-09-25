package ontology

// entry is the internal work item: copied fields, never a pointer into the
// caller's Row slice.
type entry struct {
	partition string
	value     float64
	id        string
}

// comparator returns -1/0/1 for two entries. Value order follows dir; ties
// on value always break by ID ascending.
type comparator func(a, b entry) int

// mergeSort sorts e using bottom-up iterative mergesort with the given
// comparator and returns the number of comparator invocations. It is stable,
// allocates one scratch slice, and performs at most
// n*ceil(log2(n)) comparisons per partition.
func mergeSort(e []entry, less comparator) int64 {
	n := len(e)
	if n < 2 {
		return 0
	}
	buf := make([]entry, n)
	var count int64
	src, dst := e, buf
	for width := 1; width < n; width *= 2 {
		for lo := 0; lo < n; lo += 2 * width {
			mid := lo + width
			if mid > n {
				mid = n
			}
			hi := lo + 2*width
			if hi > n {
				hi = n
			}
			i, j, k := lo, mid, lo
			for i < mid && j < hi {
				count++
				if less(src[i], src[j]) <= 0 {
					dst[k] = src[i]
					i++
				} else {
					dst[k] = src[j]
					j++
				}
				k++
			}
			for i < mid {
				dst[k] = src[i]
				i++
				k++
			}
			for j < hi {
				dst[k] = src[j]
				j++
				k++
			}
		}
		src, dst = dst, src
	}
	if &src[0] != &e[0] {
		copy(e, src)
	}
	return count
}
