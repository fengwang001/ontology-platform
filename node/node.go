// Package node defines the skip list node and its level structure.
package node

// MaxLevel caps the tower height of any node.
const MaxLevel = 32

// Node is one element of the skip list. Next[i] is the forward
// pointer at level i; len(Next) is the node's tower height.
type Node[T any] struct {
	Key  int
	Val  T
	Next []*Node[T]
}

// New builds a node with the given key, value and tower height.
func New[T any](key int, val T, level int) *Node[T] {
	return &Node[T]{Key: key, Val: val, Next: make([]*Node[T], level)}
}
