package sel

import (
	"cmp"
	"errors"
)

var (
	ErrEmpty = errors.New("sel: empty input")
	ErrBadK  = errors.New("sel: k out of range")
)

type Ordered interface {
	cmp.Ordered
}

func KthSmallest[T Ordered](arr []T, k int) (T, error) {
	value, _, err := SelectWithCount(arr, k)
	return value, err
}

func SelectWithCount[T Ordered](arr []T, k int) (T, int, error) {
	var zero T
	if len(arr) == 0 {
		return zero, 0, ErrEmpty
	}
	if k < 0 || k >= len(arr) {
		return zero, 0, ErrBadK
	}
	comparisons := 0
	seed := uint64(len(arr)) ^ uint64(k)*1099511628211
	value := selectInPlace(arr, k, 0, len(arr)-1, &comparisons)
	return value, comparisons, nil
}

func selectInPlace[T Ordered](arr []T, k, lo, hi int, comparisons *int, seeds ...uint64) T {
	for lo < hi {
		p := partition(arr, lo, hi, comparisons, &seeds[0])
		if p == k {
			return arr[p]
		}
		if p > k {
			hi = p - 1
		} else {
			lo = p + 1
		}
	}
	return arr[lo]
}

func partition[T Ordered](arr []T, lo, hi int, comparisons *int, seed *uint64) int {
	*seed = mixSeed(*seed + uint64(lo)*1099511628211 + uint64(hi)*1469598103934665603)
	pivotIndex := lo + int(*seed%uint64(hi-lo+1))
	arr[pivotIndex], arr[hi] = arr[hi], arr[pivotIndex]
	pivot := arr[hi]
	store := lo
	for i := lo; i < hi; i++ {
		*comparisons++
		if arr[i] < pivot {
			arr[store], arr[i] = arr[i], arr[store]
			store++
		}
	}
	arr[store], arr[hi] = arr[hi], arr[store]
	return store
}

func mixSeed(seed uint64) uint64 {
	seed = (seed ^ (seed >> 30)) * 0xbf58476d1ce4e5b9
	seed = (seed ^ (seed >> 27)) * 0x94d049bb133111eb
	return seed ^ (seed >> 31)
}
