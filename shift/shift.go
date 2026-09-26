// Package shift computes the Boyer–Moore bad-character table (last)
// and good-suffix table (gs). It depends on no other package.
package shift

// Table holds the two precomputed jump tables for one pattern.
type Table struct {
	Last []int // Last[c] = rightmost index of byte c in P, -1 if absent
	Good []int // Good[k]: safe shift after P[k..m) matched; Good[0] after full match
}

// Build precomputes both tables for pat (pat may be empty).
func Build(pat string) Table {
	m := len(pat)
	last := make([]int, 256)
	for i := range last {
		last[i] = -1
	}
	for i := 0; i < m; i++ {
		last[pat[i]] = i
	}
	return Table{Last: last, Good: goodSuffix(pat)}
}

// goodSuffix builds the classic strong good-suffix shift table (length m+1).
// Good[k] is the smallest safe shift when the matched suffix is P[k..m);
// Good[0] is the shift after a complete match (the pattern's least period).
func goodSuffix(p string) []int {
	m := len(p)
	gs := make([]int, m+1)
	bp := make([]int, m+1) // border positions
	i, j := m, m+1
	bp[i] = j
	for i > 0 {
		for j <= m && p[i-1] != p[j-1] {
			if gs[j] == 0 {
				gs[j] = j - i
			}
			j = bp[j]
		}
		i--
		j--
		bp[i] = j
	}
	j = bp[0]
	for k := 0; k <= m; k++ {
		if gs[k] == 0 {
			gs[k] = j
		}
		if k == j {
			j = bp[j]
		}
	}
	gs[0] = m - longestBorder(p) // full-match shift: least period
	return gs
}

// longestBorder returns the length of the longest proper prefix of p that
// is also a suffix of p.
func longestBorder(p string) int {
	m := len(p)
	pi := make([]int, m)
	for i := 1; i < m; i++ {
		b := pi[i-1]
		for b > 0 && p[i] != p[b] {
			b = pi[b-1]
		}
		if p[i] == p[b] {
			b++
		}
		pi[i] = b
	}
	if m == 0 {
		return 0
	}
	return pi[m-1]
}
