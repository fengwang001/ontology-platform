package scan

import "ontology/table"

// Scanner searches text using a compiled mismatch table.
type Scanner struct {
	table table.Table

	textAdvances int
	comparisons  int
}

// New returns a scanner for table.
func New(t table.Table) Scanner {
	return Scanner{table: t}
}

// Scan returns every zero-based occurrence start in text.
func (s *Scanner) Scan(text string) []int {
	s.textAdvances = 0
	s.comparisons = 0

	matches := make([]int, 0)
	matched := 0
	for i := 0; i < len(text); i++ {
		s.textAdvances++

		for matched > 0 {
			s.comparisons++
			if text[i] == s.table.Pattern()[matched] {
				break
			}
			matched = s.table.At(matched - 1)
		}

		if matched == 0 {
			s.comparisons++
			if text[i] == s.table.Pattern()[0] {
				matched++
			}
			continue
		}

		matched++
		if matched == s.table.Len() {
			start := i - s.table.Len() + 1
			matches = append(matches, start)
			matched = s.table.At(matched - 1)
		}
	}

	return matches
}

// TextAdvances returns the number of text cursor increments in the last scan.
func (s *Scanner) TextAdvances() int {
	return s.textAdvances
}

// Comparisons returns the number of byte comparisons in the last scan.
func (s *Scanner) Comparisons() int {
	return s.comparisons
}
