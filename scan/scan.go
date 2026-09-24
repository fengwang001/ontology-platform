package scan

import "ontology/table"

type Scanner struct {
	table                *table.Prefix
	characterComparisons int
	textAdvances         int
}

func New(t *table.Prefix) *Scanner {
	return &Scanner{table: t}
}

func (s *Scanner) Scan(text string) []int {
	s.textAdvances = 0
	s.characterComparisons = 0
	matches := []int{}
	matched := 0
	patternLen := s.table.Len()
	for i := 0; i < len(text); i++ {
		s.textAdvances++
		for matched > 0 {
			s.characterComparisons++
			if text[i] == s.table.Pattern()[matched] {
				matched++
				break
			}
			matched = s.table.At(matched)
		}
		if matched == 0 {
			s.characterComparisons++
			if text[i] == s.table.Pattern()[0] {
				matched = 1
			}
		}
		if matched == patternLen {
			matches = append(matches, i-patternLen+1)
			matched = s.table.At(matched)
		}
	}
	return matches
}

func (s *Scanner) Linear(text string) bool {
	s.Scan(text)
	return s.textAdvances == len(text) && s.characterComparisons <= 2*len(text)
}
