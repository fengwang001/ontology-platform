package nge

import "ontology/mono"

const NoAnswer = -1

type Scanner struct {
	scanner *mono.Scanner
}

func NewScanner(maxLen int) (*Scanner, error) {
	scanner, err := mono.NewScanner(maxLen)
	if err != nil {
		return nil, err
	}
	return &Scanner{scanner: scanner}, nil
}

func (s *Scanner) NextGreater(values []int) ([]int, error) {
	return s.scanner.NextGreater(values)
}

func NextGreater(values []int, maxLen int) ([]int, error) {
	return nil, nil
}

func SelfCheck(values, answers []int) bool {
	return false
}
