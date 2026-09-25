// Package check provides a naive reference implementation of three-way
// partitioning, used to validate the in-place algorithm in part.
package check

import (
	"cmp"
	"slices"

	"ontology/ord"
	"ontology/part"
)

// Naive partitions arr by counting then rearranging into
// [< pivot | == pivot | > pivot]. It is stable and uses O(n) scratch
// space, serving as the obviously-correct reference.
func Naive[T cmp.Ordered](arr []T, pivot T) (lt, gt int) {
	out := make([]T, 0, len(arr))
	for _, v := range arr {
		if v < pivot {
			out = append(out, v)
		}
	}
	lt = len(out)
	for _, v := range arr {
		if v == pivot {
			out = append(out, v)
		}
	}
	gt = len(out)
	for _, v := range arr {
		if v > pivot {
			out = append(out, v)
		}
	}
	copy(arr, out)
	return lt, gt
}

// Consistent runs part.ThreeWayPartition on a copy of arr and reports
// whether its result agrees with the naive reference: identical equal
// segment bounds, valid segment ordering, and preserved multiset.
func Consistent[T cmp.Ordered](arr []T, pivot T) bool {
	ref := slices.Clone(arr)
	rlt, rgt := Naive(ref, pivot)
	got := slices.Clone(arr)
	lt, gt := part.ThreeWayPartition(got, pivot)
	if lt != rlt || gt != rgt {
		return false
	}
	if ord.Verify(got, pivot, lt, gt) != nil {
		return false
	}
	return ord.SameMultiset(arr, got) == nil
}
