package sel

import (
	"errors"
)

type Ordered interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr |
		~float32 | ~float64 | ~string
}

var (
	ErrEmpty = errors.New("sel: empty array")
	ErrBadK  = errors.New("sel: k out of range")
)

type comparisonCounter[T Ordered] struct {
	items []T
	count int
}

func KthSmallest[T Ordered](arr []T, k int) (T, error) {
	value, _, err := KthSmallestWithStats(arr, k)
	return value, err
}

func KthSmallestWithStats[T Ordered](arr []T, k int) (T, int, error) {
	if len(arr) == 0 {
		var zero T
		return zero, 0, ErrEmpty
	}
	if k < 0 || k >= len(arr) {
		var zero T
		return zero, 0, ErrBadK
	}
	state := comparisonCounter[T]{items: arr}
	return state.quickselect(0, len(arr)-1, k), state.count, nil
}

func (s *comparisonCounter[T]) less(left, right T) bool {
	s.count++
	return left < right
}

func (s *comparisonCounter[T]) quickselect(low, high, k int) T {
	for low < high {
		p := s.partition(low, high)
		if p == k {
			return s.items[p]
		}
		if p > k {
			high = p - 1
		} else {
			low = p + 1
		}
	}
	return s.items[low]
}

func (s *comparisonCounter[T]) partition(low, high int) int {
	pivotIndex := s.medianIndex(low, high)
	pivot := s.items[pivotIndex]
	s.items[pivotIndex], s.items[high] = s.items[high], s.items[pivotIndex]

	store := low
	for i := low; i < high; i++ {
		if s.less(s.items[i], pivot) {
			s.items[i], s.items[store] = s.items[store], s.items[i]
			store++
		}
	}
	s.items[store], s.items[high] = s.items[high], s.items[store]
	return store
}

func (s *comparisonCounter[T]) medianIndex(low, high int) int {
	mid := low + (high-low)/2
	if s.less(s.items[mid], s.items[low]) {
		s.items[low], s.items[mid] = s.items[mid], s.items[low]
	}
	if s.less(s.items[high], s.items[low]) {
		s.items[low], s.items[high] = s.items[high], s.items[low]
	}
	if s.less(s.items[high], s.items[mid]) {
		s.items[mid], s.items[high] = s.items[high], s.items[mid]
	}
	return mid
}
