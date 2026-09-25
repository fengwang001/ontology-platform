// Package node defines the skip list node and its level structure.
package node

// Node is a skip list node; Next[i] is the successor at level i.
type Node[T any] struct {
	Key  int
	Val  T
	Next []*Node[T]
}

// New returns a node with the given key, value and level count.
func New[T any](key int, val T, levels int) *Node[T] {
	return &Node[T]{Key: key, Val: val, Next: make([]*Node[T], levels)}
}
