// Package match implements Rabin-Karp substring search: a rolling
// hash finds candidate positions, each hash hit is verified char by
// char so collisions never become false positives.
package match

import (
	"errors"
	"sync/atomic"

	"ontology/hash"
)

// ErrEmptyPattern is returned when pattern is empty.
var ErrEmptyPattern = errors.New("match: empty pattern")

// Default rolling-hash parameters used by FindAll.
const (
	DefaultBase = 256
	DefaultMod  = 1_000_000_007
)

var comparisons atomic.Int64

// Comparisons returns the total char comparisons spent in verify.
func Comparisons() int64 { return comparisons.Load() }

// ResetComparisons resets the verify comparison counter.
func ResetComparisons() { comparisons.Store(0) }

// FindAll returns all start positions (ascending) of pattern in text.
func FindAll(text, pattern string) []int {
	pos, _ := Find(text, pattern)
	return pos
}

// Find is FindAll plus error reporting.
func Find(text, pattern string) ([]int, error) {
	if pattern == "" {
		return nil, ErrEmptyPattern
	}
	m := len(pattern)
	if m > len(text) {
		return []int{}, nil
	}
	ph, _ := hash.New(DefaultBase, DefaultMod)
	wh, _ := hash.New(DefaultBase, DefaultMod)
	for i := 0; i < m; i++ {
		ph.Append(pattern[i])
	}
	target := ph.Value()
	out := []int{}
	for i := 0; i < len(text); i++ {
		wh.Append(text[i])
		if wh.Len() > m {
			_ = wh.Remove(text[i-m])
		}
		if wh.Len() == m && wh.Value() == target && verify(text[i-m+1:i+1], pattern) {
			out = append(out, i-m+1)
		}
	}
	return out, nil
}

// verify confirms a hash hit char by char; hash equality is a
// necessary but not sufficient condition for a match.
func verify(s, p string) bool {
	for j := 0; j < len(p); j++ {
		comparisons.Add(1)
		if s[j] != p[j] {
			return false
		}
	}
	return true
}
