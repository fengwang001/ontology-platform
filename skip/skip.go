// Package skip implements a reproducible ordered skip list index.
package skip

import (
	"errors"
	"sync/atomic"

	"ontology/node"
)

var (
	ErrBadRange  = errors.New("skip: lo >= hi")
	ErrDuplicate = errors.New("skip: duplicate key")
	ErrNotFound  = errors.New("skip: key not found")
)

// SkipList is an ordered in-memory index; read methods are race-safe.
type SkipList[T any] struct {
	head  *node.Node[T]
	level int
	n     int
	seed  uint64
	cmps  atomic.Int64
}

func New[T any](seed uint64) *SkipList[T] {
	return &SkipList[T]{head: node.Head[T](), level: 1, seed: seed}
}

func (s *SkipList[T]) Len() int { return s.n }

// Comparisons returns the total key comparisons performed by searches.
func (s *SkipList[T]) Comparisons() int64 { return s.cmps.Load() }

func (s *SkipList[T]) seek(key int, update []*node.Node[T]) *node.Node[T] {
	x := s.head
	for i := s.level - 1; i >= 0; i-- {
		for x.Next[i] != nil {
			s.cmps.Add(1)
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

func (s *SkipList[T]) Insert(key int, val T) error {
	var update [node.MaxLevel]*node.Node[T]
	if nxt := s.seek(key, update[:]); nxt != nil && nxt.Key == key {
		return ErrDuplicate
	}
	lv := node.Level(s.seed, key)
	if lv > s.level {
		for i := s.level; i < lv; i++ {
			update[i] = s.head
		}
		s.level = lv
	}
	x := node.New(key, val, lv)
	for i := 0; i < lv; i++ {
		x.Next[i], update[i].Next[i] = update[i].Next[i], x
	}
	s.n++
	return nil
}

func (s *SkipList[T]) Find(key int) (val T, ok bool) {
	if x := s.seek(key, nil); x != nil && x.Key == key {
		return x.Val, true
	}
	return val, false
}

func (s *SkipList[T]) Delete(key int) error {
	var update [node.MaxLevel]*node.Node[T]
	x := s.seek(key, update[:])
	if x == nil || x.Key != key {
		return ErrNotFound
	}
	for i := 0; i < len(x.Next); i++ {
		update[i].Next[i] = x.Next[i]
	}
	for s.level > 1 && s.head.Next[s.level-1] == nil {
		s.level--
	}
	s.n--
	return nil
}

func (s *SkipList[T]) Range(lo, hi int) ([]T, error) {
	if lo >= hi {
		return nil, ErrBadRange
	}
	var out []T
	for x := s.seek(lo, nil); x != nil && x.Key < hi; x = x.Next[0] {
		out = append(out, x.Val)
	}
	return out, nil
}

func (s *SkipList[T]) Levels() [][]int {
	lv := make([][]int, s.level)
	for i := range lv {
		for x := s.head.Next[i]; x != nil; x = x.Next[i] {
			lv[i] = append(lv[i], x.Key)
		}
	}
	return lv
}
