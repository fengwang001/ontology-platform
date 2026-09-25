// Package check provides a naive sort-based reference for quickselect.
package check

import (
	"slices"

	"ontology/ord"
	"ontology/sel"
)

// KthSmallestRef sorts a copy of arr and returns the element at index k.
// It shares sel's error contract so tests can compare results directly.
func KthSmallestRef[T ord.Ordered](arr []T, k int) (T, error) {
	var zero T
	if len(arr) == 0 {
		return zero, sel.ErrEmpty
	}
	if k < 0 || k >= len(arr) {
		return zero, sel.ErrBadK
	}
	s := slices.Clone(arr)
	slices.Sort(s)
	return s[k], nil
}
