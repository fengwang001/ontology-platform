// Package fibseq owns the Fibonacci number table F(1)=1, F(2)=2, F(3)=3, ...
// and Zeckendorf decompositions over that table. It depends on no other
// package in this module.
package fibseq

import (
	"errors"
	"sort"
)

// maxInt64 is math.MaxInt64, spelled out to avoid an extra import.
const maxInt64 = int64(^uint64(0) >> 1)

// ErrNotPositive is returned when Zeckendorf is handed 0 or a negative value.
var ErrNotPositive = errors.New("fibseq: n must be a positive integer")

// ErrOverflow is returned when a Fibonacci reconstruction exceeds int64.
var ErrOverflow = errors.New("fibseq: value overflows int64")

// fullTable holds every Fibonacci number representable in int64:
// fullTable[i-1] == F(i). The first F that would overflow int64 is omitted.
var fullTable = buildTable(maxInt64)

// buildTable returns F(1)..F(k) with every entry <= limit; the loop stops
// before the first entry that overflows int64 or exceeds limit.
func buildTable(limit int64) []int64 {
	if limit < 1 {
		return nil
	}
	t := []int64{1}
	if limit < 2 {
		return t
	}
	t = append(t, 2)
	for {
		next := t[len(t)-1] + t[len(t)-2]
		if next <= t[len(t)-1] || next > limit { // wrapped past int64, or above limit
			break
		}
		t = append(t, next)
	}
	return t
}

// Table returns F(1)..F(k), all <= limit, in ascending order. The returned
// slice is freshly allocated; callers may modify it.
func Table(limit int64) []int64 {
	if limit < 1 {
		return nil
	}
	return append([]int64(nil), buildTable(limit)...)
}

// Zeckendorf returns the ascending 1-based indices of the pairwise
// non-adjacent Fibonacci numbers summing to n (greedy, largest first).
func Zeckendorf(n int64) ([]int, error) {
	if n <= 0 {
		return nil, ErrNotPositive
	}
	tab := buildTable(n)
	var idx []int
	for n > 0 {
		i := sort.Search(len(tab), func(j int) bool { return tab[j] > n }) - 1
		n -= tab[i]
		idx = append(idx, i+1) // collected largest first
	}
	for l, r := 0, len(idx)-1; l < r; l, r = l+1, r-1 {
		idx[l], idx[r] = idx[r], idx[l]
	}
	return idx, nil
}

// SumIndices reconstructs n = sum F(i). It returns an overflow error when an
// index names a Fibonacci number that does not fit int64, or the running sum
// leaves the int64 range. Indices may be supplied in any order.
func SumIndices(idx []int) (int64, error) {
	var sum int64
	for _, i := range idx {
		if i < 1 || i > len(fullTable) {
			return 0, ErrOverflow
		}
		f := fullTable[i-1]
		if sum > maxInt64-f {
			return 0, ErrOverflow
		}
		sum += f
	}
	return sum, nil
}

// MaxIndex returns the largest 1-based index i for which F(i) fits int64.
func MaxIndex() int { return len(fullTable) }
