// Package node 定义跳表节点：每层一个后继指针与对应跨度。
package node

import "ontology/key"

// Node 是跳表节点。Next[i] 是第 i 层后继，Span[i] 是该指针
// 恰好跨过的底层元素个数；Next[i] 为 nil 时 Span[i] 是到末尾的元素数。
type Node struct {
	Key  key.Key
	Next []*Node
	Span []int
}

// New 创建一个层高为 level 的节点。
func New(k key.Key, level int) *Node {
	return &Node{Key: k, Next: make([]*Node, level), Span: make([]int, level)}
}

// Level 返回节点层高。
func (n *Node) Level() int { return len(n.Next) }
