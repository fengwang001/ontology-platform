package check

import (
	"slices"

	"ontology/sel"
)

func NaiveKthSmallest[T sel.Ordered](arr []T, k int) (T, error) {
	var zero T
	if len(arr) == 0 {
		return zero, sel.ErrEmpty
	}
	if k < 0 || k >= len(arr) {
		return zero, sel.ErrBadK
	}
	sorted := slices.Clone(arr)
	slices.Sort(sorted)
	return sorted[k], nil
}
