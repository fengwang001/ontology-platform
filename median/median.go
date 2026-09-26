// Package median locates the weighted median by walking the wmid treap:
// at each node it adds the whole left subtree's weight instead of
// rescanning elements, so the query is O(log m) rather than O(m log m).
package median

import (
	"errors"
	"sync/atomic"

	"ontology/wmid"
)

// ErrEmpty is returned by Median on an empty set.
var ErrEmpty = errors.New("median: median of empty set")

// Finder owns one value-ordered tree. The zero value is ready to use.
type Finder struct {
	tree  wmid.Tree
	steps atomic.Int64 // nodes visited by the most recent Median call; unexported
}

// Insert delegates to the underlying tree (rejected inserts leave it
// untouched).
func (f *Finder) Insert(value, weight int64) error {
	return f.tree.Insert(value, weight)
}

// Total is W, the sum of all inserted weights (0 when empty).
func (f *Finder) Total() int64 {
	if r := f.tree.Root(); r != nil {
		return r.Sum
	}
	return 0
}

// Median returns the smallest value whose ordered weight prefix P satisfies
// P >= W/2 (compared over reals, here as the exact integer 2*P >= W).
func (f *Finder) Median() (int64, error) {
	root := f.tree.Root()
	if root == nil {
		f.steps.Store(0)
		return 0, ErrEmpty
	}
	w := root.Sum
	var acc int64 // total weight strictly below the current subtree's values
	n := root
	f.steps.Store(0)
	for n != nil {
		f.steps.Add(1)
		left := subtreeSum(n.Left)
		// The qualifying node lies inside the left subtree.
		if 2*(acc+left) >= w {
			n = n.Left
			continue
		}
		// Smallest prefix reaching W/2 ends at this node.
		if 2*(acc+left+n.Weight) >= w {
			return n.Value, nil
		}
		// This node and everything smaller stay below W/2: descend right.
		acc += left + n.Weight
		n = n.Right
	}
	return 0, ErrEmpty // unreachable for a non-empty tree
}

// StepBoundHolds reports whether, for every requested size m, one Median
// over m freshly inserted elements traverses at most
// 2*ceil(log2(m))+2 nodes. Only the verdict crosses the package boundary;
// the node count itself stays an unexported field.
func StepBoundHolds(sizes []int) bool {
	for _, m := range sizes {
		var f Finder
		for v := 0; v < m; v++ {
			if err := f.Insert(int64(v), 1); err != nil {
				return false
			}
		}
		if _, err := f.Median(); err != nil {
			return false
		}
		if f.steps.Load() > int64(2*ceilLog2(m)+2) {
			return false
		}
	}
	return true
}

func ceilLog2(m int) int {
	c, p := 0, 1
	for p < m {
		p <<= 1
		c++
	}
	return c
}

func subtreeSum(n *wmid.Node) int64 {
	if n == nil {
		return 0
	}
	return n.Sum
}
