// Package check provides a naive sort-based reference implementation of
// kth-smallest, used to validate sel.KthSmallest.
package check

import (
	"cmp"
	"slices"
)

// KthRef returns the kth smallest (0-based) element of arr by sorting a
// copy. Callers must ensure 0 <= k < len(arr).
func KthRef[T cmp.Ordered](arr []T, k int) T {
	cp := slices.Clone(arr)
	slices.Sort(cp)
	return cp[k]
}
