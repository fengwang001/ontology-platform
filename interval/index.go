// Package interval provides a concurrency-safe augmented index for
// int64 half-open intervals [lo, hi).
//
// The index is an AVL self-balancing BST keyed by (lo, id); every node
// maintains the maximum hi of its subtree (maxHi) so that Stab and
// Overlap only visit branches that may intersect: with no hits the
// number of examined nodes is O(log n), independent of the total
// number of intervals.
package interval

import (
	"errors"
	"sync"
	"sync/atomic"
)

// Distinguishable rejection reasons; test with errors.Is.
var (
	// ErrNonPositiveID: Insert id is not a positive integer.
	ErrNonPositiveID = errors.New("interval: id must be a positive integer")
	// ErrInvalidRange: Insert interval does not satisfy lo < hi.
	ErrInvalidRange = errors.New("interval: lo must be less than hi")
	// ErrDuplicateID: Insert id already exists.
	ErrDuplicateID = errors.New("interval: id already exists")
	// ErrIDNotFound: Remove id does not exist.
	ErrIDNotFound = errors.New("interval: id not found")
	// ErrInvalidQuery: Overlap query does not satisfy a < b.
	ErrInvalidQuery = errors.New("interval: query requires a < b")
)

// node is an AVL tree node keyed by (lo, id); maxHi is the maximum hi
// in the node's subtree.
type node struct {
	lo, hi int64
	id     int64
	maxHi  int64
	height int
	left   *node
	right  *node
}

// Index is a concurrency-safe augmented index of half-open intervals.
// The zero value is not ready for use; create one with New.
type Index struct {
	mu       sync.RWMutex
	root     *node
	byID     map[int64]int64 // id -> lo, locates the tree node by id
	size     int
	examined atomic.Int64 // 非导出计数器：最近一次查询考察的节点数
}

// New creates an empty index.
func New() *Index {
	return &Index{byID: make(map[int64]int64)}
}

// Len returns the number of intervals in the index.
func (ix *Index) Len() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.size
}

// Insert adds interval [lo, hi) with the given id. The id must be a
// positive integer that does not already exist, and lo must be less
// than hi. Violations are reported in the order: non-positive id,
// lo >= hi, duplicate id; only the first is returned and a rejected
// Insert leaves the index unchanged.
func (ix *Index) Insert(id, lo, hi int64) error {
	if id <= 0 {
		return ErrNonPositiveID
	}
	if lo >= hi {
		return ErrInvalidRange
	}
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if _, ok := ix.byID[id]; ok {
		return ErrDuplicateID
	}
	ix.root = insert(ix.root, lo, hi, id)
	ix.byID[id] = lo
	ix.size++
	return nil
}

// Remove deletes the interval with the given id. A missing id yields
// ErrIDNotFound and leaves the index unchanged.
func (ix *Index) Remove(id int64) error {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	lo, ok := ix.byID[id]
	if !ok {
		return ErrIDNotFound
	}
	ix.root = remove(ix.root, lo, id)
	delete(ix.byID, id)
	ix.size--
	return nil
}

// Stab returns the ids of all intervals with lo <= x < hi, ordered by
// (lo, id) ascending.
func (ix *Index) Stab(x int64) []int64 {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	var (
		out   []int64
		count int
	)
	stab(ix.root, x, &out, &count)
	ix.examined.Store(int64(count))
	return out
}

// Overlap returns the ids of all intervals intersecting [a, b), i.e.
// lo < b and a < hi, ordered by (lo, id) ascending. It returns
// ErrInvalidQuery when a >= b.
func (ix *Index) Overlap(a, b int64) ([]int64, error) {
	if a >= b {
		return nil, ErrInvalidQuery
	}
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	var (
		out   []int64
		count int
	)
	overlap(ix.root, a, b, &out, &count)
	ix.examined.Store(int64(count))
	return out, nil
}

// lastExamined 返回最近一次 Stab/Overlap 考察的节点数，仅供测试
// 验证无命中时考察量不随区间总数线性增长。
func (ix *Index) lastExamined() int64 {
	return ix.examined.Load()
}

// stab 收集中序（即 (lo, id) 升序）下满足 lo <= x < hi 的编号。
// lo > x 的节点其右子树 lo 均大于 x，整支剪除；左子树仅当
// maxHi > x 时才可能含命中，否则同样剪除。
func stab(n *node, x int64, out *[]int64, count *int) {
	if n == nil {
		return
	}
	*count++
	if maxHiOf(n.left) > x {
		stab(n.left, x, out, count)
	}
	if n.lo > x {
		return
	}
	if x < n.hi {
		*out = append(*out, n.id)
	}
	stab(n.right, x, out, count)
}

// overlap 收集中序下满足 lo < b 且 a < hi 的编号。
// lo >= b 的节点其右子树 lo 均不小于 b，整支剪除；左子树仅当
// maxHi > a 时才可能含命中。
func overlap(n *node, a, b int64, out *[]int64, count *int) {
	if n == nil {
		return
	}
	*count++
	if maxHiOf(n.left) > a {
		overlap(n.left, a, b, out, count)
	}
	if n.lo >= b {
		return
	}
	if a < n.hi {
		*out = append(*out, n.id)
	}
	overlap(n.right, a, b, out, count)
}
