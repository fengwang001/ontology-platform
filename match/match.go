package match

import (
	"errors"
	"sync/atomic"

	"ontology/hash"
)

var ErrEmptyPattern = errors.New("match: empty pattern")

const (
	base = int64(2)
	mod  = int64(251)
)

var verificationCount atomic.Int64

func FindAll(text, pattern string) ([]int, error) {
	if pattern == "" {
		return nil, ErrEmptyPattern
	}
	if len(pattern) > len(text) {
		return []int{}, nil
	}

	m := len(pattern)
	verificationCount.Store(0)
	patternHash, _ := hash.New(base, mod, m)
	windowHash, _ := hash.New(base, mod, m)
	for i := 0; i < m; i++ {
		patternHash.Append(pattern[i])
		windowHash.Append(text[i])
	}

	matches := make([]int, 0)
	for start := 0; start <= len(text)-m; start++ {
		if start > 0 {
			windowHash.Remove(text[start-1], text[start+m-1])
		}
		if windowHash.Value() == patternHash.Value() && equalAt(text, pattern, start) {
			matches = append(matches, start)
		}
	}

	return matches, nil
}

func equalAt(text, pattern string, start int) bool {
	for offset := range pattern {
		verificationCount.Add(1)
		if text[start+offset] != pattern[offset] {
			return false
		}
	}
	return true
}

func VerificationCount() int {
	return int(verificationCount.Load())
}
