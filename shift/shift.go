// Package shift computes the Boyer–Moore shift tables: the bad-character
// last-occurrence table and the good-suffix minimal-safe-shift table.
package shift

// BadChar returns last[c] = the rightmost index of byte c in p, or -1.
func BadChar(p []byte) []int {
	last := make([]int, 256)
	for i := range last {
		last[i] = -1
	}
	for i, c := range p {
		last[c] = i
	}
	return last
}

// suffixes returns suff[i] = length of the longest string that is both a
// suffix of p[:i+1] and a suffix of p.
func suffixes(p []byte) []int {
	m := len(p)
	suff := make([]int, m)
	suff[m-1] = m
	f, g := m-1, m-1
	for i := m - 2; i >= 0; i-- {
		if i > g && suff[i+m-1-f] < i-g {
			suff[i] = suff[i+m-1-f]
		} else {
			if i < g {
				g = i
			}
			f = i
			for g >= 0 && p[g] == p[g+m-1-f] {
				g--
			}
			suff[i] = f - g
		}
	}
	return suff
}

// zvals returns z[i] = length of the longest common prefix of p and p[i:].
func zvals(p []byte) []int {
	m := len(p)
	z := make([]int, m)
	l, r := 0, 0
	for i := 1; i < m; i++ {
		if i < r {
			z[i] = min(r-i, z[i-l])
		}
		for i+z[i] < m && p[z[i]] == p[i+z[i]] {
			z[i]++
		}
		if i+z[i] > r {
			l, r = i, i+z[i]
		}
	}
	return z
}

// GoodSuffix returns gs[0..m]: gs[k] is the minimal safe shift when the
// already-matched suffix is p[k:] (mismatch at index k-1); gs[0] is the
// shift after a full match and equals the minimal period of p (or m).
func GoodSuffix(p []byte) []int {
	m := len(p)
	suff := suffixes(p)
	z := zvals(p)
	gs := make([]int, m+1)
	for k := 0; k <= m; k++ {
		gs[k] = m
		for d := 1; d < m; d++ {
			ok := false
			if d <= k {
				// Another occurrence of p[k:] starts at k-d inside p.
				i := m - 1 - d
				ok = suff[i] >= m-k && (k-1-d < 0 || p[k-1-d] != p[k-1])
			} else {
				// A prefix of p aligns with a suffix of p[k:].
				ok = z[d] >= m-d
			}
			if ok {
				gs[k] = d
				break
			}
		}
	}
	return gs
}
