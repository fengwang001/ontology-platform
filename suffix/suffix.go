// Package suffix builds the suffix array of a byte string.
// It depends on no other package in this module.
package suffix

// Build returns the suffix array of s: for every k, SA[k] is the starting
// index of the k-th lexicographically smallest suffix (byte-wise; a proper
// prefix sorts before the longer string). Empty input yields a nil slice.
//
// It uses prefix doubling with counting (radix) sorts: O(n log n) time.
func Build(s []byte) []int {
	n := len(s)
	if n == 0 {
		return nil
	}
	sa := make([]int, n)
	rank := make([]int, n)
	tmp := make([]int, n)

	// Initial ordering: counting sort by the single first byte.
	var cnt [256]int
	for _, b := range s {
		cnt[b]++
	}
	for i := 1; i < 256; i++ {
		cnt[i] += cnt[i-1]
	}
	for i := n - 1; i >= 0; i-- {
		cnt[s[i]]--
		sa[cnt[s[i]]] = i
	}
	classes := 1
	rank[sa[0]] = 0
	for i := 1; i < n; i++ {
		if s[sa[i]] != s[sa[i-1]] {
			classes++
		}
		rank[sa[i]] = classes - 1
	}

	for k := 1; k < n && classes < n; k <<= 1 {
		// Indices ordered by the second key of the pair (rank[i], rank[i+k]);
		// suffixes shorter than i+k have second key -1 and come first.
		p := 0
		for i := n - k; i < n; i++ {
			tmp[p] = i
			p++
		}
		for i := 0; i < n; i++ {
			if sa[i] >= k {
				tmp[p] = sa[i] - k
				p++
			}
		}
		// Stable counting sort by the first key (rank).
		buckets := make([]int, classes)
		for i := 0; i < n; i++ {
			buckets[rank[tmp[i]]]++
		}
		sum := 0
		for i := range buckets {
			t := buckets[i]
			buckets[i] = sum
			sum += t
		}
		for i := 0; i < n; i++ {
			idx := tmp[i]
			c := rank[idx]
			sa[buckets[c]] = idx
			buckets[c]++
		}
		// Recompute equivalence classes from the sorted pairs.
		tmp[sa[0]] = 0
		newClasses := 1
		for i := 1; i < n; i++ {
			a, b := sa[i-1], sa[i]
			diff := rank[a] != rank[b] ||
				(a+k < n) != (b+k < n) ||
				(a+k < n && rank[a+k] != rank[b+k])
			if diff {
				newClasses++
			}
			tmp[b] = newClasses - 1
		}
		rank, tmp = tmp, rank
		classes = newClasses
	}
	return sa
}
