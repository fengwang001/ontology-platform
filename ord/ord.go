// Package ord defines the ordered-type constraint and the sentinel
// errors used to validate three-way partition results.
package ord

import (
	"cmp"
	"errors"
	"fmt"

	"ontology/part"
)

// Ordered is the constraint for values comparable with <, ==, >.
type Ordered = cmp.Ordered

// Sentinel errors, distinguishable with errors.Is.
var (
	ErrBadRange     = errors.New("ord: lt/gt out of range")
	ErrWrongSegment = errors.New("ord: segment violates ordering")
	ErrLostElements = errors.New("ord: elements lost or duplicated")
)

// Verify checks that arr is correctly three-way partitioned around
// pivot with equal segment [lt, gt).
func Verify[T Ordered](arr []T, pivot T, lt, gt int) error {
	if lt < 0 || lt > gt || gt > len(arr) {
		return fmt.Errorf("%w: lt=%d gt=%d n=%d", ErrBadRange, lt, gt, len(arr))
	}
	for i, v := range arr {
		switch {
		case i < lt && !(v < pivot):
			return fmt.Errorf("%w: arr[%d]=%v not < %v", ErrWrongSegment, i, v, pivot)
		case lt <= i && i < gt && v != pivot:
			return fmt.Errorf("%w: arr[%d]=%v not == %v", ErrWrongSegment, i, v, pivot)
		case i >= gt && !(v > pivot):
			return fmt.Errorf("%w: arr[%d]=%v not > %v", ErrWrongSegment, i, v, pivot)
		}
	}
	return nil
}

// SameMultiset checks that a and b contain the same elements with the
// same multiplicities.
func SameMultiset[T Ordered](a, b []T) error {
	if len(a) != len(b) {
		return fmt.Errorf("%w: len %d != %d", ErrLostElements, len(a), len(b))
	}
	counts := make(map[T]int, len(a))
	for _, v := range a {
		counts[v]++
	}
	for _, v := range b {
		counts[v]--
		if counts[v] < 0 {
			return fmt.Errorf("%w: extra %v", ErrLostElements, v)
		}
	}
	return nil
}

// Checked runs part.ThreeWayPartition on arr and verifies the result
// against a snapshot of the original input.
func Checked[T Ordered](arr []T, pivot T) (lt, gt int, err error) {
	orig := make([]T, len(arr))
	copy(orig, arr)
	lt, gt = part.ThreeWayPartition(arr, pivot)
	if err := Verify(arr, pivot, lt, gt); err != nil {
		return lt, gt, err
	}
	if err := SameMultiset(orig, arr); err != nil {
		return lt, gt, err
	}
	return lt, gt, nil
}
