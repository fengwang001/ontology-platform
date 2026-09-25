// Package skip implements a reproducible ordered skip list.
package skip

import (
	"encoding/binary"
	"errors"
	"math/bits"
	"ontology/node"
	"sync/atomic"
)

var ErrBadRange = errors.New("skip: lo >= hi")
var ErrDuplicate = errors.New("skip: duplicate key")
var ErrNotFound = errors.New("skip: key not found")

type List[T any] struct {
	head     *node.Node[T]
	seed     uint64
	length   int
	compares atomic.Int64
}

func New[T any](seed uint64) *List[T] {
	return &List[T]{head: node.New[T](0, *new(T), node.MaxLevel), seed: seed}
}

func levelOf(seed uint64, key int) int {
	h := (uint64(key) ^ seed) * 0x9E3779B97F4A7C15
	h ^= h >> 29
	h *= 0xBF58476D1CE4E5B9
	h ^= h >> 32
	return min(bits.TrailingZeros64(h)+1, node.MaxLevel)
}

func (l *List[T]) seek(key int) [node.MaxLevel]*node.Node[T] {
	var update [node.MaxLevel]*node.Node[T]
	cur := l.head
	for i := node.MaxLevel - 1; i >= 0; i-- {
		for cur.Next[i] != nil && cur.Next[i].Key < key {
			l.compares.Add(1)
			cur = cur.Next[i]
		}
		update[i] = cur
	}
	return update
}

func (l *List[T]) Insert(key int, val T) error {
	update := l.seek(key)
	if n := update[0].Next[0]; n != nil && n.Key == key {
		return ErrDuplicate
	}
	n := node.New(key, val, levelOf(l.seed, key))
	for i := range n.Next {
		n.Next[i], update[i].Next[i] = update[i].Next[i], n
	}
	l.length++
	return nil
}

func (l *List[T]) Find(key int) (val T, ok bool) {
	if n := l.seek(key)[0].Next[0]; n != nil && n.Key == key {
		return n.Val, true
	}
	return
}

func (l *List[T]) Delete(key int) error {
	update := l.seek(key)
	n := update[0].Next[0]
	if n == nil || n.Key != key {
		return ErrNotFound
	}
	for i := range n.Next {
		update[i].Next[i] = n.Next[i]
	}
	l.length--
	return nil
}

func (l *List[T]) Range(lo, hi int) ([]int, error) {
	if lo >= hi {
		return nil, ErrBadRange
	}
	var keys []int
	for n := l.seek(lo)[0].Next[0]; n != nil && n.Key < hi; n = n.Next[0] {
		keys = append(keys, n.Key)
	}
	return keys, nil
}

func (l *List[T]) Len() int        { return l.length }
func (l *List[T]) Compares() int64 { return l.compares.Load() }
func (l *List[T]) Serialize() []byte {
	var b []byte
	for n := l.head.Next[0]; n != nil; n = n.Next[0] {
		b = append(binary.AppendVarint(b, int64(n.Key)), byte(len(n.Next)))
	}
	return b
}
