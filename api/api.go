// Package api is the outward-facing layer: it validates and pins a string
// once (New), then serves read-only queries. It depends on package cmp.
//
// A *String is immutable after construction, so all methods are safe for
// concurrent use without locking. Every rejected operation fails as a
// whole with a decidable sentinel error and leaves no trace.
package api

import (
	"errors"
	"fmt"

	"ontology/cmp"
	"ontology/cyc"
)

// maxLen bounds the accepted string length.
const maxLen = 1 << 20

// Sentinel errors, one per rejection cause; distinguish with errors.Is.
var (
	ErrEmpty    = errors.New("api: empty string")
	ErrTooLong  = errors.New("api: string exceeds maxLen")
	ErrBadIndex = errors.New("api: rotation index out of range")
)

// String is a validated, immutable byte string.
type String struct {
	s string
}

// New validates s and pins it. On rejection it returns a sentinel error
// and no value; no state is created or mutated anywhere.
func New(s string) (*String, error) {
	if len(s) == 0 {
		return nil, ErrEmpty
	}
	if len(s) > maxLen {
		return nil, fmt.Errorf("%w: len=%d > %d", ErrTooLong, len(s), maxLen)
	}
	return &String{s: s}, nil
}

// MinRotation returns the starting index of the lexicographically
// smallest rotation. The error result is always nil for a validated
// instance; it is kept so callers can chain error handling uniformly.
func (st *String) MinRotation() (int, error) {
	return cyc.MinRotation(st.s), nil
}

// Rotate returns s[k:] + s[:k]. An out-of-range k is rejected with
// ErrBadIndex before any work; the instance is untouched.
func (st *String) Rotate(k int) (string, error) {
	if k < 0 || k >= len(st.s) {
		return "", fmt.Errorf("%w: k=%d, len=%d", ErrBadIndex, k, len(st.s))
	}
	return cyc.Rotate(st.s, k), nil
}

// CyclicEqual reports whether t is a rotation of the pinned string.
// A length mismatch (or any other non-rotation) yields false, not an error.
func (st *String) CyclicEqual(t string) bool {
	return cmp.CyclicEqual(st.s, t)
}

// SelfCheck verifies the four invariants from NOTES.md against a built-in
// corpus, using a naive O(n^2) oracle as ground truth. It returns the
// first violation found, or nil. It mutates nothing.
func (st *String) SelfCheck() error {
	corpus := []string{st.s, "baabaa", "banana", "bca", "aaaa", "abab", "z", "aaabb"}
	for _, s := range corpus {
		k := cyc.MinRotation(s)
		if want := naiveMinRotation(s); k != want {
			return fmt.Errorf("selfcheck: MinRotation(%q)=%d, naive=%d", s, k, want)
		}
		if rot := cyc.Rotate(s, k); rot != s[k:]+s[:k] {
			return fmt.Errorf("selfcheck: Rotate(%q,%d)=%q is not the real rotation", s, k, rot)
		}
		for j := 0; j < len(s); j++ {
			if !cmp.CyclicEqual(s, s[j:]+s[:j]) {
				return fmt.Errorf("selfcheck: CyclicEqual(%q, its rotation @%d)=false", s, j)
			}
		}
		if cmp.CyclicEqual(s, s+"x") {
			return fmt.Errorf("selfcheck: CyclicEqual(%q, longer string)=true", s)
		}
	}
	if _, err := New(""); !errors.Is(err, ErrEmpty) {
		return fmt.Errorf("selfcheck: New(\"\") err=%v, want ErrEmpty", err)
	}
	if _, err := st.Rotate(-1); !errors.Is(err, ErrBadIndex) {
		return fmt.Errorf("selfcheck: Rotate(-1) err=%v, want ErrBadIndex", err)
	}
	if k, err := st.MinRotation(); err != nil || cyc.Rotate(st.s, k) != st.s[k:]+st.s[:k] {
		return fmt.Errorf("selfcheck: instance misbehaves after rejected ops")
	}
	return nil
}

// naiveMinRotation is the O(n^2) ground truth: enumerate all rotations,
// keep the lexicographically smallest, ties broken by the smaller index.
func naiveMinRotation(s string) int {
	best := 0
	for k := 1; k < len(s); k++ {
		if s[k:]+s[:k] < s[best:]+s[:best] {
			best = k
		}
	}
	return best
}
