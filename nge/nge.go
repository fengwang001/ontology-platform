// Package nge is the public entry point for next-greater-element queries.
// It depends on mono (which depends on stack); the dependency direction is
// one way only.
package nge

import "ontology/mono"

// None marks a position without a satisfying element on its right.
const None = mono.None

// Sentinel errors are re-exported so callers can make decidable checks.
var (
	ErrNilInput = mono.ErrNilInput
	ErrTooLong  = mono.ErrTooLong
	ErrBadLimit = mono.ErrBadLimit
)

// NextGreater returns, for every position i of a, the smallest index
// j > i such that a[j] is the next element STRICTLY greater than a[i]
// (equal values do not satisfy the relation). Positions with no such
// element receive None (-1), which is never a legal index.
//
// maxLen is the configurable upper bound on len(a); it must be positive.
// A rejected call returns no partial result and never touches a.
func NextGreater(a []int, maxLen int) ([]int, error) {
	sc, err := mono.NewScanner(maxLen)
	if err != nil {
		return nil, err
	}
	return sc.Scan(a)
}

// SelfCheck verifies ans against a: each answered slot points at the
// earliest strictly greater element on the right, and every None slot has
// strictly nothing greater to its right. It re-checks every position with a
// direct scan, so it is independent of the monotone-stack implementation.
func SelfCheck(a, ans []int) bool {
	if len(a) != len(ans) {
		return false
	}
	for i, j := range ans {
		if j == None {
			for k := i + 1; k < len(a); k++ {
				if a[k] > a[i] {
					return false
				}
			}
			continue
		}
		if j <= i || j >= len(a) || a[j] <= a[i] {
			return false
		}
		for k := i + 1; k < j; k++ {
			if a[k] > a[i] {
				return false
			}
		}
	}
	return true
}
