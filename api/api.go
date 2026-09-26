// Package api is the outward face: validated, immutable cyclic strings.
package api

import (
	"errors"

	"ontology/cmp"
	"ontology/cyc"
)

// maxLen is the largest accepted string length.
const maxLen = 1 << 20

// Sentinel errors, all mutually distinct.
var (
	ErrEmpty   = errors.New("api: empty string")
	ErrTooLong = errors.New("api: string length exceeds maxLen")
	ErrIndex   = errors.New("api: rotation index out of range")
)

// String is an immutable validated string; safe for concurrent reads.
type String struct {
	s string
}

// New validates s and fixes it. Rejects empty and over-long strings.
func New(s string) (*String, error) {
	if len(s) == 0 {
		return nil, ErrEmpty
	}
	if len(s) > maxLen {
		return nil, ErrTooLong
	}
	return &String{s: s}, nil
}

// MinRotation returns the start index of the minimal rotation.
func (x *String) MinRotation() (int, error) {
	return cyc.MinRotation(x.s), nil
}

// Rotate returns s[k:] + s[:k]; rejects out-of-range k.
func (x *String) Rotate(k int) (string, error) {
	if k < 0 || k >= len(x.s) {
		return "", ErrIndex
	}
	return cyc.Rotate(x.s, k), nil
}

// CyclicEqual reports whether t is a rotation of the fixed string.
func (x *String) CyclicEqual(t string) bool {
	return cmp.CyclicEqual(x.s, t)
}

// naiveMinRotation is the O(n^2) reference: enumerate all rotations,
// pick the lexicographic minimum, ties to the smallest index.
func naiveMinRotation(s string) int {
	best := 0
	for k := 1; k < len(s); k++ {
		if s[k:]+s[:k] < s[best:]+s[:best] {
			best = k
		}
	}
	return best
}

// SelfCheck verifies the four invariants on built-in strings.
func (x *String) SelfCheck() bool {
	samples := []string{"baabaa", "banana", "bca", "aaaa", "abacaba", "z"}
	for _, s := range samples {
		v, err := New(s)
		if err != nil {
			return false
		}
		k, err := v.MinRotation()
		if err != nil || k != naiveMinRotation(s) { // invariant 1
			return false
		}
		r, err := v.Rotate(k)
		if err != nil || r != s[k:]+s[:k] { // invariant 2
			return false
		}
		for j := 0; j < len(s); j++ { // invariant 3
			if !v.CyclicEqual(s[j:]+s[:j]) || v.CyclicEqual(s+"x") {
				return false
			}
		}
	}
	if _, err := New(""); !errors.Is(err, ErrEmpty) { // invariant 4
		return false
	}
	if _, err := x.Rotate(-1); !errors.Is(err, ErrIndex) {
		return false
	}
	k, err := x.MinRotation() // still usable after rejections
	return err == nil && k == naiveMinRotation(x.s)
}
