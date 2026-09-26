// Package bm runs the Boyer–Moore search loop: compare right to left,
// advance by max(bad-character shift, good-suffix shift).
package bm

import "ontology/shift"

// Searcher holds the precomputed shift tables for one pattern.
// Search is not safe for concurrent use; the tables are read-only.
type Searcher struct {
	pat  []byte
	last []int // bad-character table: rightmost occurrence in pat, -1 if absent
	gs   []int // good-suffix table: gs[k] for matched suffix pat[k:], gs[0] after full match
	cmp  int   // character pairs compared during the last Search
}

// New precomputes both tables for p (must be non-empty).
func New(p []byte) *Searcher {
	return &Searcher{pat: p, last: shift.BadChar(p), gs: shift.GoodSuffix(p)}
}

// Search returns all start indices of the pattern in text, including
// overlapping occurrences. If more than limit matches exist it stops early
// and reports over = true.
func (s *Searcher) Search(text []byte, limit int) (matches []int, over bool) {
	s.cmp = 0
	m, n := len(s.pat), len(text)
	for pos := 0; pos+m <= n; {
		j := m - 1
		for j >= 0 {
			s.cmp++
			if s.pat[j] != text[pos+j] {
				break
			}
			j--
		}
		if j < 0 {
			matches = append(matches, pos)
			if len(matches) > limit {
				return matches, true
			}
			pos += s.gs[0]
			continue
		}
		pos += max(j-s.last[text[pos+j]], s.gs[j+1])
	}
	return matches, false
}
