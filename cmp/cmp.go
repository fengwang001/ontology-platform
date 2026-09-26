// Package cmp decides cyclic equivalence via minimal rotations.
package cmp

import "ontology/cyc"

// CyclicEqual reports whether a and b are rotations of each other:
// equal length and identical minimal representations.
func CyclicEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}
	ka, kb := cyc.MinRotation(a), cyc.MinRotation(b)
	return cyc.Rotate(a, ka) == cyc.Rotate(b, kb)
}
