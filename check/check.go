// Package check provides a naive sort-based reference implementation
// used to validate the quickselect in package sel.
package check

import (
	"cmp"
	"slices"
)

// KthSmallestRef returns the k-th smallest element (0-based) by fully
// sorting a copy of arr: the O(n log n) baseline that sel must match.
func KthSmallestRef[T cmp.Ordered](arr []T, k int) T {
	cp := slices.Clone(arr)
	slices.Sort(cp)
	return cp[k]
}
