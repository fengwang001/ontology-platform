// Package agg incrementally maintains COUNT/SUM/MIN/MAX per group under an insert/retract stream.
package agg

import (
	"errors"
	"math"
	"math/rand"

	"ontology/delta"
)

var ErrSumOverflow = errors.New("agg: sum overflow")

// node is one distinct value with cnt live copies; treap keyed by val, heap by prio.
type node struct {
	val         int64
	cnt         int
	prio        uint64
	left, right *node
}

// Group is one key's live multiset; visited is unexported and absent from the public API.
type Group struct {
	root    *node
	count   int64
	sum     int64
	visited int
	rng     *rand.Rand
}

func NewGroup() *Group { return &Group{rng: rand.New(rand.NewSource(1))} }

func merge(a, b *node) *node {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	case a.prio > b.prio:
		a.right = merge(a.right, b)
		return a
	}
	b.left = merge(a, b.left)
	return b
}

func (g *Group) insert(n *node, v int64) *node {
	if n == nil {
		return &node{val: v, cnt: 1, prio: g.rng.Uint64()}
	}
	g.visited++
	switch {
	case v == n.val:
		n.cnt++
	case v < n.val:
		n.left = g.insert(n.left, v)
		if n.left.prio > n.prio { // right rotation
			a := n.left
			n.left, a.right = a.right, n
			n = a
		}
	default:
		n.right = g.insert(n.right, v)
		if n.right.prio > n.prio { // left rotation
			a := n.right
			n.right, a.left = a.left, n
			n = a
		}
	}
	return n
}

// erase removes one copy of v; a missing v changes nothing and returns false.
func (g *Group) erase(n *node, v int64) (*node, bool) {
	if n == nil {
		return nil, false
	}
	g.visited++
	switch {
	case v < n.val:
		c, ok := g.erase(n.left, v)
		if !ok {
			return n, false
		}
		n.left = c
	case v > n.val:
		c, ok := g.erase(n.right, v)
		if !ok {
			return n, false
		}
		n.right = c
	case n.cnt > 1:
		n.cnt--
		return n, true
	default:
		return merge(n.left, n.right), true
	}
	return n, true
}

// Apply applies one event atomically: on error no field has changed.
func (g *Group) Apply(ev delta.Event) error {
	if !ev.Valid() {
		return delta.ErrInvalidEvent
	}
	g.visited = 0
	if ev.Op == delta.Retract { // bounds checked before mutation, as for insert
		if (ev.Val > 0 && g.sum < math.MinInt64+ev.Val) ||
			(ev.Val < 0 && g.sum > math.MaxInt64+ev.Val) {
			return ErrSumOverflow
		}
		r, ok := g.erase(g.root, ev.Val) // the miss path mutates nothing
		if !ok {
			return delta.ErrRetractMissing
		}
		g.root, g.count, g.sum = r, g.count-1, g.sum-ev.Val
		return nil
	}
	if (ev.Val > 0 && g.sum > math.MaxInt64-ev.Val) ||
		(ev.Val < 0 && g.sum < math.MinInt64-ev.Val) {
		return ErrSumOverflow
	}
	g.sum += ev.Val
	g.count++
	g.root = g.insert(g.root, ev.Val)
	return nil
}

// Value returns COUNT/SUM/MIN/MAX; minOK/maxOK false on an empty group (absent != 0).
func (g *Group) Value() (cnt, sum, min, max int64, minOK, maxOK bool) {
	cnt, sum = g.count, g.sum
	for l := g.root; l != nil; l = l.left {
		min, minOK = l.val, true
	}
	for r := g.root; r != nil; r = r.right {
		max, maxOK = r.val, true
	}
	return
}

// SublinearVisit seeds m inserts, applies one more, and reports whether stored
// nodes entered stayed within a small constant times log2(m); verdict only.
func SublinearVisit(m int) bool {
	g := NewGroup()
	for i := 0; i < m; i++ {
		_ = g.Apply(delta.Event{Val: int64(i), Op: delta.Insert})
	}
	_ = g.Apply(delta.Event{Val: -1, Op: delta.Insert})
	return float64(g.visited) <= 8*math.Log2(float64(m))
}
