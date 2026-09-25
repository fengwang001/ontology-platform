// Package node 定义跳表节点与层级上限，不依赖其他包。
package node

// MaxLevel 是节点层数上限，p=1/2 几何分布下足够支撑 2^32 量级的元素。
const MaxLevel = 32

// Node 是跳表节点，Next[i] 是第 i 层的前向指针，len(Next) 即节点层数。
type Node[T any] struct {
	Key  int
	Val  T
	Next []*Node[T]
}

// New 创建一个层数为 level 的节点。
func New[T any](key int, val T, level int) *Node[T] {
	return &Node[T]{Key: key, Val: val, Next: make([]*Node[T], level)}
}
