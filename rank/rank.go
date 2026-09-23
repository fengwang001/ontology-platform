// Package rank answers rank queries on the skip list by hopping along
// spans: k-th element, rank of a key, and range counts.
package rank

import (
	"errors"
	"sync/atomic"

	"ontology/key"
	"ontology/list"
)

var (
	ErrOutOfRange = errors.New("rank: index out of range")
	ErrBadRange   = errors.New("rank: lo > hi")
)

// Rank performs rank queries on a list. The unexported visited counter
// records how many nodes the most recent query hopped through; it is
// atomic so concurrent read-only queries stay race-free.
type Rank struct {
	l       *list.List
	visited atomic.Int64
}

// New binds rank queries to l.
func New(l *list.List) *Rank { return &Rank{l: l} }

// LastVisited reports the node visits of the most recent query.
// Diagnostic only; not part of the multiset's public API.
func (r *Rank) LastVisited() int64 { return r.visited.Load() }

// At returns the k-th element (0-based) or ErrOutOfRange.
func (r *Rank) At(k int) (key.Key, error) {
	if k < 0 || k >= r.l.Len() {
		return "", ErrOutOfRange
	}
	r.visited.Store(0)
	x, pos := r.l.Header(), -1 // header sits at bottom index -1
	for i := r.l.Level() - 1; i >= 0; i-- {
		for x.Next(i) != nil && pos+x.Span(i) < k {
			pos += x.Span(i)
			x = x.Next(i)
			r.visited.Add(1)
		}
	}
	return x.Next(0).Key, nil
}

// RankOf returns the count of elements strictly less than k: the first
// occurrence index if k exists, its insertion position otherwise.
func (r *Rank) RankOf(k key.Key) int {
	r.visited.Store(0)
	x, pos := r.l.Header(), 0
	for i := r.l.Level() - 1; i >= 0; i-- {
		for x.Next(i) != nil && x.Next(i).Key.Compare(k) < 0 {
			pos += x.Span(i)
			x = x.Next(i)
			r.visited.Add(1)
		}
	}
	return pos
}

// Range returns the number of elements x with lo <= x < hi.
func (r *Rank) Range(lo, hi key.Key) (int, error) {
	if lo.Compare(hi) > 0 {
		return 0, ErrBadRange
	}
	return r.RankOf(hi) - r.RankOf(lo), nil
}
