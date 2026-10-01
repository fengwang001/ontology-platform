// Package intervalindex provides a concurrency-safe augmented index for
// half-open int64 intervals [lo, hi).
//
// Intervals live in a treap ordered by (lo, id); every subtree aggregates
// minLo, maxLo and maxHi so query traversals can prune subtrees that cannot
// match. A no-hit query therefore examines only O(log n) vertices instead of
// scanning all n intervals.
package intervalindex

import (
	"errors"
	"sync"
	"sync/atomic"
)

// Distinguishable rejection reasons.
var (
	ErrNonPositiveID = errors.New("intervalindex: id must be a positive integer")
	ErrEmptyInterval = errors.New("intervalindex: lo must be less than hi")
	ErrDuplicateID   = errors.New("intervalindex: id already exists")
	ErrIDNotFound    = errors.New("intervalindex: id does not exist")
	ErrInvalidQuery  = errors.New("intervalindex: query range must satisfy a < b")
)

// Index stores intervals with positive integer ids and supports point
// stabbing and range overlap queries. The zero value is not usable; use New.
type Index struct {
	mu   sync.RWMutex
	root *node
	byID map[int64]*node

	// count is the cumulative number of interval records inspected by
	// queries since the last reset. Pruned subtrees are not counted.
	count atomic.Int64
}

// New returns an empty index.
func New() *Index {
	return &Index{byID: make(map[int64]*node)}
}

// Insert adds the interval [lo, hi) tagged with id. Rejections are reported
// in a fixed order: non-positive id, empty interval, duplicate id. A rejected
// call leaves the index unchanged.
func (idx *Index) Insert(id, lo, hi int64) error {
	if id <= 0 {
		return ErrNonPositiveID
	}
	if lo >= hi {
		return ErrEmptyInterval
	}
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if _, ok := idx.byID[id]; ok {
		return ErrDuplicateID
	}
	n := &node{lo: lo, hi: hi, id: id, priority: priorityOf(lo, hi, id)}
	idx.root = insert(idx.root, n)
	idx.byID[id] = n
	return nil
}

// Remove deletes the interval carrying id.
func (idx *Index) Remove(id int64) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	n, ok := idx.byID[id]
	if !ok {
		return ErrIDNotFound
	}
	idx.root = erase(idx.root, n.lo, n.id)
	delete(idx.byID, id)
	return nil
}

// Stab returns every interval satisfying lo <= x < hi, ordered by (lo, id).
func (idx *Index) Stab(x int64) []int64 {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	var out []int64
	seen := 0
	idx.root.stab(x, &out, &seen)
	idx.count.Add(int64(seen))
	return out
}

// Overlap returns every interval intersecting [a, b), ordered by (lo, id).
// Half-open intersection means lo < b && a < hi; it returns ErrInvalidQuery
// when a >= b and leaves the index unchanged.
func (idx *Index) Overlap(a, b int64) ([]int64, error) {
	if a >= b {
		return nil, ErrInvalidQuery
	}
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	var out []int64
	seen := 0
	idx.root.overlap(a, b, &out, &seen)
	idx.count.Add(int64(seen))
	return out, nil
}

// Len reports the number of stored intervals.
func (idx *Index) Len() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.byID)
}

// examinedCount reports how many interval records queries have inspected
// since the last reset. Records inside pruned subtrees are not counted.
func (idx *Index) examinedCount() int64 {
	return idx.count.Load()
}

// resetExamined zeroes the cumulative inspection counter.
func (idx *Index) resetExamined() {
	idx.count.Store(0)
}

// stab appends, in (lo, id) order, every interval satisfying lo <= x < hi.
func (n *node) stab(x int64, out *[]int64, seen *int) {
	if n == nil {
		return
	}
	// The left subtree can contain x only if one of its intervals passes x;
	// maxHi aggregates exactly that fact.
	if n.left != nil && n.left.maxHi > x {
		n.left.stab(x, out, seen)
	}
	*seen++
	if n.lo <= x && x < n.hi {
		*out = append(*out, n.id)
	}
	// The right subtree starts no earlier than this node's lo; it can matter
	// only if some interval there starts at or before x.
	if n.right != nil && n.right.minLo <= x {
		n.right.stab(x, out, seen)
	}
}

// overlap appends, in (lo, id) order, every interval with lo < b && hi > a.
func (n *node) overlap(a, b int64, out *[]int64, seen *int) {
	if n == nil {
		return
	}
	// Left subtree can intersect only if one of its intervals ends after a.
	if n.left != nil && n.left.maxHi > a {
		n.left.overlap(a, b, out, seen)
	}
	*seen++
	if n.lo < b && n.hi > a {
		*out = append(*out, n.id)
	}
	// Right subtree can intersect only if one of its intervals starts
	// before b.
	if n.right != nil && n.right.minLo < b {
		n.right.overlap(a, b, out, seen)
	}
}

// priorityOf derives a deterministic treap priority. Replaying the same call
// sequence therefore rebuilds the same tree shape and the same behavior.
func priorityOf(lo, hi, id int64) uint64 {
	return splitmix64(uint64(id) ^
		splitmix64(uint64(lo)^0x9e3779b97f4a7c15) ^
		splitmix64(uint64(hi)^0xd1b54a32d192ed03))
}

// splitmix64 is the deterministic finalizer of the SplitMix64 generator.
func splitmix64(z uint64) uint64 {
	z += 0x9e3779b97f4a7c15
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}
