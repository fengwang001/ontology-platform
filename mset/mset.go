// Package mset is the public ordered multiset. It wires together list,
// rank and iter, and hosts the structural self-check and the
// structure-equality comparison used to prove order-independence.
package mset

import (
	"fmt"

	"ontology/iter"
	"ontology/key"
	"ontology/list"
	"ontology/rank"
)

// Mset is an ordered multiset backed by a deterministic skip list.
type Mset struct {
	l *list.List
	r *rank.Rank
}

// New creates an empty multiset with the given resource limits.
func New(maxLevel, maxElems int) *Mset {
	l := list.New(maxLevel, maxElems)
	return &Mset{l: l, r: rank.New(l)}
}

func (m *Mset) Insert(k key.Key) error { return m.l.Insert(k) }
func (m *Mset) Delete(k key.Key) error { return m.l.Delete(k) }
func (m *Mset) Count(k key.Key) int    { return m.l.Count(k) }
func (m *Mset) Len() int               { return m.l.Len() }

// At returns the k-th element (0-based) or rank.ErrOutOfRange.
func (m *Mset) At(k int) (key.Key, error) { return m.r.At(k) }

// RankOf returns the number of elements strictly less than k.
func (m *Mset) RankOf(k key.Key) int { return m.r.RankOf(k) }

// Range returns the count of elements x with lo <= x < hi.
func (m *Mset) Range(lo, hi key.Key) (int, error) { return m.r.Range(lo, hi) }

// Iterate returns a fail-fast iterator over the multiset.
func (m *Mset) Iterate() *iter.Iter { return iter.New(m.l) }

// SameStructure reports whether both multisets are field-by-field
// identical: same keys, same tower heights, same spans, same links.
func (m *Mset) SameStructure(o *Mset) bool {
	a, b := m.l, o.l
	if a.Level() != b.Level() || a.Len() != b.Len() {
		return false
	}
	x, y := a.Header(), b.Header()
	for x != nil && y != nil {
		if x.Key != y.Key || x.Levels() != y.Levels() {
			return false
		}
		for i := 0; i < x.Levels(); i++ {
			if x.Span(i) != y.Span(i) || (x.Next(i) == nil) != (y.Next(i) == nil) {
				return false
			}
		}
		x, y = x.Next(0), y.Next(0)
	}
	return x == nil && y == nil
}

// Check verifies: every span equals the number of bottom-level elements
// its link crosses, every level is ordered, and the bottom-level element
// count equals the recorded size.
func (m *Mset) Check() error {
	l := m.l
	n := 0
	for x := l.Header().Next(0); x != nil; x = x.Next(0) {
		n++
	}
	if n != l.Len() {
		return fmt.Errorf("mset: length %d but counted %d", l.Len(), n)
	}
	for i := 0; i < l.Level(); i++ {
		x := l.Header()
		for x.Next(i) != nil {
			y := x.Next(i)
			if x != l.Header() && y.Key.Compare(x.Key) < 0 {
				return fmt.Errorf("mset: level %d out of order", i)
			}
			d := 0
			for z := x; z != y; z = z.Next(0) {
				d++
			}
			if d != x.Span(i) {
				return fmt.Errorf("mset: level %d span %d, want %d", i, x.Span(i), d)
			}
			x = y
		}
		if x.Span(i) != 0 {
			return fmt.Errorf("mset: level %d tail span %d, want 0", i, x.Span(i))
		}
	}
	return nil
}
