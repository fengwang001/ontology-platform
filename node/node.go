// Package node 定义跳表节点与层级结构，不依赖其他包。
package node

// Node 是跳表中的一个节点。forward[l] 指向第 l 层的后继。
type Node[T any] struct {
	Key     int
	Val     T
	forward []*Node[T]
}

// New 创建层级为 level（forward 数组长度为 level）的节点。
func New[T any](key int, val T, level int) *Node[T] {
	return &Node[T]{Key: key, Val: val, forward: make([]*Node[T], level)}
}

// Level 返回节点拥有的层数（forward 指针数量）。
func (n *Node[T]) Level() int { return len(n.forward) }

// Next 返回第 level 层（0 起）的后继，越界返回 nil。
func (n *Node[T]) Next(level int) *Node[T] {
	if level < 0 || level >= len(n.forward) {
		return nil
	}
	return n.forward[level]
}

// SetNext 设置第 level 层后继，越界时静默忽略（调用方保证合法）。
func (n *Node[T]) SetNext(level int, next *Node[T]) {
	if level >= 0 && level < len(n.forward) {
		n.forward[level] = next
	}
}
