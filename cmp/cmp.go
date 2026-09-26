// Package cmp decides cyclic equivalence of two strings via their
// lexicographically minimal rotations. It depends only on package cyc.
package cmp

import "ontology/cyc"

// CyclicEqual reports whether a and b are cyclic rotations of each other:
// same length, and a equals some rotation of b. Two strings are cyclic
// rotations of each other iff their minimal rotations are identical, so
// comparing canonical forms is both necessary and sufficient.
// Strings of different lengths (including one empty, one not) are never
// cyclically equal; the function returns false rather than an error.
func CyclicEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true // the empty string is trivially a rotation of itself
	}
	ka, kb := cyc.MinRotation(a), cyc.MinRotation(b)
	return cyc.Rotate(a, ka) == cyc.Rotate(b, kb)
}
