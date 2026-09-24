// Package scan runs a one-pass KMP scan of a text using a failure table,
// reporting every (including overlapping) match start. The text pointer
// never moves backwards.
package scan

import "ontology/table"

// Scanner is a single-use matcher: one Scan call consumes it.
type Scanner struct {
	pat string
	tab table.Table

	advances int // text-pointer advances (unexported, proof of no backtracking)
	compares int // character comparisons (unexported, proof of linearity)
}

// New returns a Scanner for pattern pat with its failure table tab.
func New(pat string, tab table.Table) *Scanner {
	return &Scanner{pat: pat, tab: tab}
}

// Scan returns all start positions where pat occurs in text.
// The caller must guarantee len(pat) <= len(text).
func (s *Scanner) Scan(text string) []int {
	var hits []int
	j := 0
	for i := 0; i < len(text); {
		s.compares++
		if text[i] == s.pat[j] {
			i++
			s.advances++
			j++
			if j == len(s.pat) {
				hits = append(hits, i-j)
				j = s.tab.Fallback(j)
			}
		} else if j > 0 {
			j = s.tab.Fallback(j)
		} else {
			i++
			s.advances++
		}
	}
	return hits
}
