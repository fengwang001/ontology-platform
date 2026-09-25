package match

import (
	"errors"

	"ontology/hash"
)

var ErrEmptyPattern = errors.New("empty pattern")

type Stats struct {
	Checks     int
	Collisions int
}

func FindAll(text, pattern string) ([]int, error) {
	positions, _, err := FindAllStats(text, pattern)
	return positions, err
}

func FindAllStats(text, pattern string) ([]int, Stats, error) {
	if pattern == "" {
		return nil, Stats{}, ErrEmptyPattern
	}
	m := len(pattern)
	if m > len(text) {
		return []int{}, Stats{}, nil
	}

	patternHash := hash.New(hash.Base, hash.Mod, m)
	windowHash := hash.New(hash.Base, hash.Mod, m)
	for i := range m {
		patternHash.Append(pattern[i])
		windowHash.Append(text[i])
	}

	positions := []int{}
	stats := Stats{}
	for start := 0; start+m <= len(text); start++ {
		if windowHash.Value() == patternHash.Value() {
			match := true
			for offset := range m {
				stats.Checks++
				if text[start+offset] != pattern[offset] {
					match = false
					break
				}
			}
			if match {
				positions = append(positions, start)
			} else {
				stats.Collisions++
			}
		}
		if start+m < len(text) {
			windowHash.Remove(text[start], text[start+m])
		}
	}
	return positions, stats, nil
}
