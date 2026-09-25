package ontology

// compareStrings is the lexicographic -1/0/1 comparison over string IDs.
func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// sortStrings sorts keys in lexicographic order using the same O(n log n)
// bottom-up mergesort, keeping the key path comparison-free of maps.
func sortStrings(keys []string) {
	n := len(keys)
	if n < 2 {
		return
	}
	buf := make([]string, n)
	src, dst := keys, buf
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
				if compareStrings(src[i], src[j]) <= 0 {
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
	if &src[0] != &keys[0] {
		copy(keys, src)
	}
}
