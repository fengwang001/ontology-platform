// Package node defines skip list nodes and deterministic level generation.
package node

import "math/bits"

// MaxLevel caps the tower height of any node.
const MaxLevel = 32

// Node is a skip list node; Next[i] links level i, len(Next) is its height.
type Node[T any] struct {
	Key  int
	Val  T
	Next []*Node[T]
}

// New returns a node of the given height.
func New[T any](key int, val T, level int) *Node[T] {
	return &Node[T]{Key: key, Val: val, Next: make([]*Node[T], level)}
}

// Head returns the sentinel head node with full height.
func Head[T any]() *Node[T] {
	return &Node[T]{Next: make([]*Node[T], MaxLevel)}
}

// Level maps (seed, key) to a tower height in [1, MaxLevel] following a
// p=1/2 geometric distribution. It is a pure function, so the structure
// of a list depends only on the seed and the key set, never on runtime
// randomness or insertion order.
func Level(seed uint64, key int) int {
	h := uint64(key)*0x9E3779B97F4A7C15 ^ seed*0xBF58476D1CE4E5B9
	h ^= h >> 29
	h *= 0x94D049BB133111EB
	h ^= h >> 32
	l := bits.TrailingZeros64(h) + 1
	if l > MaxLevel {
		l = MaxLevel
	}
	return l
}
