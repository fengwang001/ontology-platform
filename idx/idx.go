// Package idx provides the inner-table index: ascending-order validation
// plus binary-search bounds (first >= k and first > k). It depends on
// nothing outside the standard library.
package idx

import "errors"

// Sentinel errors: distinguishable via errors.Is, never aliased.
var (
	ErrNilInput    = errors.New("idx: nil input slice")
	ErrNegativeKey = errors.New("idx: negative key")
	ErrNotSorted   = errors.New("idx: keys not in ascending order")
)

// Index is an immutable ascending index over the inner relation S.
type Index struct {
	keys []int // ascending, duplicates adjacent; a private copy of S
}

// Build validates keys (non-nil, all >= 0, ascending) and returns an
// index over a private copy. Any rejection fails the whole batch and
// leaves no state behind.
func Build(keys []int) (*Index, error) {
	if keys == nil {
		return nil, ErrNilInput
	}
	for i, k := range keys {
		if k < 0 {
			return nil, ErrNegativeKey
		}
		if i > 0 && keys[i-1] > k {
			return nil, ErrNotSorted
		}
	}
	cp := make([]int, len(keys))
	copy(cp, keys)
	return &Index{keys: cp}, nil
}

// Len returns the number of indexed entries.
func (x *Index) Len() int { return len(x.keys) }

// At returns the key at sorted position i.
func (x *Index) At(i int) int { return x.keys[i] }

// Bounds returns the match interval [lo, hi) for key k: lo is the first
// position with key >= k (binary search), hi the first with key > k
// (scanned from lo; duplicates are adjacent, so this is output-sized).
// observe, when non-nil, is called with each index entry compared while
// locating k; it lets the caller count locating comparisons without the
// index exposing any counter of its own.
func (x *Index) Bounds(k int, observe func(i int)) (lo, hi int) {
	lo, hi = 0, len(x.keys)
	for lo < hi {
		mid := lo + (hi-lo)/2
		if observe != nil {
			observe(mid)
		}
		if x.keys[mid] < k {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	for hi = lo; hi < len(x.keys) && x.keys[hi] == k; hi++ {
	}
	return lo, hi
}
