// Package skip implements a reproducible ordered skip list.
package skip

import (
	"errors"
	"fmt"
	"math/rand"
	"sync/atomic"

	"ontology/node"
)

var ErrBadRange = errors.New("skip: lo >= hi")
var ErrDuplicate = errors.New("skip: duplicate key")
var maxLevel = 32

type List[T any] struct {
	head *node.Node[T]
	n    int
	rng  *rand.Rand
	cmps atomic.Int64
}

func New[T any](seed uint64) *List[T] {
	return &List[T]{head: node.New[T](0, *new(T), maxLevel), rng: rand.New(rand.NewSource(int64(seed)))}
}
func (l *List[T]) Len() int           { return l.n }
func (l *List[T]) Comparisons() int64 { return l.cmps.Load() }
func (l *List[T]) search(key int, update []*node.Node[T]) *node.Node[T] {
	x := l.head
	for i := maxLevel - 1; i >= 0; i-- {
		for x.Next[i] != nil {
			l.cmps.Add(1)
			if x.Next[i].Key >= key {
				break
			}
			x = x.Next[i]
		}
		if update != nil {
			update[i] = x
		}
	}
	return x.Next[0]
}

func (l *List[T]) Insert(key int, val T) error {
	update := make([]*node.Node[T], maxLevel)
	if x := l.search(key, update); x != nil && x.Key == key {
		return ErrDuplicate
	}
	lvl := 1
	for lvl < maxLevel && l.rng.Uint64()&1 == 1 {
		lvl++
	}
	x := node.New(key, val, lvl)
	for i := 0; i < lvl; i++ {
		x.Next[i], update[i].Next[i] = update[i].Next[i], x
	}
	l.n++
	return nil
}
func (l *List[T]) Find(key int) (T, bool) {
	if x := l.search(key, nil); x != nil && x.Key == key {
		return x.Val, true
	}
	return *new(T), false
}
func (l *List[T]) Delete(key int) bool {
	update := make([]*node.Node[T], maxLevel)
	x := l.search(key, update)
	if x == nil || x.Key != key {
		return false
	}
	for i := 0; i < maxLevel && update[i].Next[i] == x; i++ {
		update[i].Next[i] = x.Next[i]
	}
	l.n--
	return true
}
func (l *List[T]) Range(lo, hi int) ([]int, error) {
	if lo >= hi {
		return nil, ErrBadRange
	}
	var keys []int
	for x := l.search(lo, nil); x != nil && x.Key < hi; x = x.Next[0] {
		keys = append(keys, x.Key)
	}
	return keys, nil
}
func (l *List[T]) Serialize() string {
	s := ""
	for i := maxLevel - 1; i >= 0; i-- {
		s += fmt.Sprintf("L%d:", i)
		for x := l.head.Next[i]; x != nil; x = x.Next[i] {
			s += fmt.Sprintf("%d,", x.Key)
		}
		s += "\n"
	}
	return s
}
