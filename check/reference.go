package check

import (
	"cmp"
	"slices"

	"ontology/sel"
)

type Ordered interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr |
		~float32 | ~float64 | ~string
}

func KthSmallestSorted[T Ordered](arr []T, k int) (T, error) {
	if len(arr) == 0 {
		var zero T
		return zero, sel.ErrEmpty
	}
	if k < 0 || k >= len(arr) {
		var zero T
		return zero, sel.ErrBadK
	}
	ordered := append([]T(nil), arr...)
	slices.SortFunc(ordered, cmp.Compare[T])
	return ordered[k], nil
}
