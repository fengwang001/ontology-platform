// Package list is the deterministic skip list: find, insert, delete,
// and the exact per-level span invariant that ranking relies on.
package list

import (
	"errors"

	"ontology/key"
	"ontology/node"
)

var (
	ErrNotFound = errors.New("list: key not found")
	ErrFull     = errors.New("list: element limit reached")
	ErrMaxLevel = errors.New("list: key level exceeds max level")
)

// List is a skip list whose node levels derive deterministically from keys.
// Not safe for concurrent writes; concurrent reads are safe.
type List struct {
	header   *node.Node
	level    int
	length   int
	maxLevel int
	maxElems int
	version  uint64
}

// New creates an empty list with the given resource limits.
func New(maxLevel, maxElems int) *List {
	return &List{header: node.New("", maxLevel), level: 1, maxLevel: maxLevel, maxElems: maxElems}
}

func (l *List) Len() int           { return l.length }
func (l *List) Level() int         { return l.level }
func (l *List) Version() uint64    { return l.version }
func (l *List) Header() *node.Node { return l.header }

// find returns, per level, the last node with key < k, plus bottom ranks.
func (l *List) find(k key.Key) (update []*node.Node, rank []int) {
	update, rank = make([]*node.Node, l.maxLevel), make([]int, l.maxLevel)
	x := l.header
	for i := l.level - 1; i >= 0; i-- {
		if i < l.level-1 {
			rank[i] = rank[i+1]
		}
		for x.Next(i) != nil && x.Next(i).Key.Compare(k) < 0 {
			rank[i] += x.Span(i)
			x = x.Next(i)
		}
		update[i] = x
	}
	return update, rank
}

// Insert adds one copy of k. Duplicates are allowed and get identical towers.
func (l *List) Insert(k key.Key) error {
	if l.length >= l.maxElems {
		return ErrFull
	}
	lvl := k.Level()
	if lvl > l.maxLevel {
		return ErrMaxLevel
	}
	update, rank := l.find(k)
	if lvl > l.level {
		for i := l.level; i < lvl; i++ {
			update[i] = l.header
		}
		l.level = lvl
	}
	nn := node.New(k, lvl)
	for i := 0; i < lvl; i++ {
		nn.SetNext(i, update[i].Next(i))
		if nn.Next(i) != nil {
			nn.SetSpan(i, update[i].Span(i)-(rank[0]-rank[i]))
		}
		update[i].SetNext(i, nn)
		update[i].SetSpan(i, rank[0]-rank[i]+1)
	}
	for i := lvl; i < l.level; i++ {
		if update[i].Next(i) != nil {
			update[i].SetSpan(i, update[i].Span(i)+1)
		}
	}
	l.length++
	l.version++
	return nil
}

// Delete removes one copy of k, merging its spans back into its predecessors.
func (l *List) Delete(k key.Key) error {
	update, _ := l.find(k)
	target := update[0].Next(0)
	if target == nil || !target.Key.Equal(k) {
		return ErrNotFound
	}
	for i := 0; i < l.level; i++ {
		if update[i].Next(i) == target {
			if target.Next(i) == nil {
				update[i].SetSpan(i, 0) // nil links cross nothing
			} else {
				update[i].SetSpan(i, update[i].Span(i)+target.Span(i)-1)
			}
			update[i].SetNext(i, target.Next(i))
		} else if update[i].Next(i) != nil {
			update[i].SetSpan(i, update[i].Span(i)-1)
		}
	}
	for l.level > 1 && l.header.Next(l.level-1) == nil {
		l.level--
	}
	l.length--
	l.version++
	return nil
}

// Count returns how many copies of k the list holds.
func (l *List) Count(k key.Key) int {
	update, _ := l.find(k)
	n := 0
	for x := update[0].Next(0); x != nil && x.Key.Equal(k); x = x.Next(0) {
		n++
	}
	return n
}
