// Package rmq implements range-minimum queries and point updates on a seg.Tree.
package rmq

import (
	"errors"
	"math/rand"
	"sync/atomic"

	"ontology/seg"
)

// Four distinct, decidable sentinel failures.
var (
	ErrEmptyInput       = errors.New("rmq: cannot build from empty array")
	ErrInvalidRange     = errors.New("rmq: query range has l > r")
	ErrRangeOutOfBounds = errors.New("rmq: query range out of bounds")
	ErrIndexOutOfBounds = errors.New("rmq: update index out of bounds")
	ErrTooManyVisits    = errors.New("rmq: query visited more nodes than O(log n) bound")
)

// Structure is a built segment tree with point-updateable leaves.
type Structure struct {
	tree *seg.Tree
	n    int
	// visited counts nodes read by the most recent Query; unexported and
	// never exposed through any exported function or method.
	visited atomic.Int64
}

// Build pads arr up to the next power of two; padding leaves keep the
// seg.Inf that NewTree pre-fills, so they cannot pollute any minimum.
func Build(arr []int64) (*Structure, error) {
	if len(arr) == 0 {
		return nil, ErrEmptyInput
	}
	leaves := 1
	for leaves < len(arr) {
		leaves <<= 1
	}
	t := seg.NewTree(leaves)
	for i, v := range arr {
		t.Set(t.Leaf(i), v)
	}
	for i := leaves - 1; i >= 1; i-- {
		t.Set(i, min64(t.At(seg.Left(i)), t.At(seg.Right(i))))
	}
	return &Structure{tree: t, n: len(arr)}, nil
}

// Size reports the logical array length n.
func (s *Structure) Size() int { return s.n }

// Query returns the minimum over the half-open interval [l, r). Query(l,l)
// returns seg.Inf. Every check precedes any write, so a rejected call
// changes neither the tree nor the visited counter.
func (s *Structure) Query(l, r int) (int64, error) {
	if l > r {
		return 0, ErrInvalidRange
	}
	if l < 0 || r > s.n {
		return 0, ErrRangeOutOfBounds
	}
	if seg.Len(l, r) == 0 { // empty half-open interval: +Inf identity
		s.visited.Store(0)
		return seg.Inf, nil
	}
	res := seg.Inf
	lo, hi := s.tree.Leaf(l), s.tree.Leaf(r) // half-open at leaf level
	visited := int64(0)
	for lo < hi {
		if lo&1 == 1 { // right child: take it, step past
			res = min64(res, s.tree.At(lo))
			visited++
			lo++
		}
		if hi&1 == 1 { // hi exclusive: pull left, take that node
			hi--
			res = min64(res, s.tree.At(hi))
			visited++
		}
		lo, hi = seg.Parent(lo), seg.Parent(hi)
	}
	s.visited.Store(visited)
	return res, nil
}

// Update changes leaf i to v and recomputes every ancestor up to the root.
// The bounds check precedes the first write, so a rejected call is a no-op.
func (s *Structure) Update(i int, v int64) error {
	if i < 0 || i >= s.n {
		return ErrIndexOutOfBounds
	}
	p := s.tree.Leaf(i)
	s.tree.Set(p, v)
	for p = seg.Parent(p); p >= 1; p = seg.Parent(p) {
		s.tree.Set(p, min64(s.tree.At(seg.Left(p)), s.tree.At(seg.Right(p))))
	}
	return nil
}

// VerifyLogAccess returns a non-nil verdict unless, for several n in
// [100,10000], every sampled full-range and small random query visits at
// most 4*ceil(log2(n))+4 nodes. The counter value itself is never returned.
func VerifyLogAccess() error {
	for _, n := range []int{100, 317, 1000, 3163, 10000} {
		arr := make([]int64, n)
		rng := rand.New(rand.NewSource(int64(n)))
		for i := range arr {
			arr[i] = rng.Int63()
		}
		s, err := Build(arr)
		if err != nil {
			return err
		}
		bound := int64(4*ceilLog2(n) + 4)
		queries := [][2]int{{0, n}, {0, 1}, {n / 2, n/2 + 1}}
		for k := 0; k < 16; k++ { // loop-generated small random intervals
			l := rng.Intn(n)
			span := 8
			if span > n-l {
				span = n - l
			}
			queries = append(queries, [2]int{l, l + rng.Intn(span)})
		}
		for _, q := range queries {
			if _, err := s.Query(q[0], q[1]); err != nil {
				return err
			}
			if s.visited.Load() > bound {
				return ErrTooManyVisits
			}
		}
	}
	return nil
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func ceilLog2(n int) int {
	k := 0
	for p := 1; p < n; p <<= 1 {
		k++
	}
	return k
}
