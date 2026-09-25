// Package check holds a naive ordered reference and test helpers.
package check

import (
	"slices"

	"ontology/skip"
)

// Ref is a naive ordered reference backed by a map.
type Ref map[int]int

// Keys returns the keys in [lo, hi) in ascending order.
func (r Ref) Keys(lo, hi int) []int {
	var ks []int
	for k := range r {
		if k >= lo && k < hi {
			ks = append(ks, k)
		}
	}
	slices.Sort(ks)
	return ks
}

func iota(n int) []int {
	ks := make([]int, n)
	for i := range ks {
		ks[i] = i
	}
	return ks
}

func build(seed uint64, keys []int) *skip.List[int] {
	l := skip.New[int](seed)
	for _, k := range keys {
		l.Insert(k, k)
	}
	return l
}
