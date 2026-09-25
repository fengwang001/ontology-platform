package check

import "errors"

var ErrEmptyPattern = errors.New("empty pattern")

func Naive(text, pattern string) ([]int, error) {
	if pattern == "" {
		return nil, ErrEmptyPattern
	}
	if len(pattern) > len(text) {
		return []int{}, nil
	}

	positions := []int{}
	for start := 0; start+len(pattern) <= len(text); start++ {
		if text[start:start+len(pattern)] == pattern {
			positions = append(positions, start)
		}
	}
	return positions, nil
}
