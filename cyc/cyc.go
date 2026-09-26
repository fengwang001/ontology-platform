// Package cyc implements Booth's O(n) lexicographically minimal rotation.
package cyc

import "sync/atomic"

// cmpCount records character-pair comparisons made by MinRotation.
// Unexported on purpose: it must never leak into the public API.
var cmpCount atomic.Int64

// MinRotation returns the smallest starting index k of the
// lexicographically minimal rotation of s. Ties (periodic strings)
// resolve to the smallest k. s must be non-empty.
func MinRotation(s string) int {
	n := len(s)
	cmpCount.Store(0)
	i, j, k := 0, 1, 0
	for i < n && j < n && k < n {
		a, b := s[(i+k)%n], s[(j+k)%n]
		cmpCount.Add(1)
		switch {
		case a == b:
			k++
		case a > b:
			i = i + k + 1
			if i == j {
				j++
			}
			k = 0
		default:
			j = j + k + 1
			if i == j {
				i++
			}
			k = 0
		}
	}
	if i < j {
		return i
	}
	return j
}

// Rotate returns the rotation s[k:] + s[:k]. k must be in [0, len(s)).
func Rotate(s string, k int) string {
	return s[k:] + s[:k]
}
