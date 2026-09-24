// Package node 定义跳表节点：每层的后继指针与跨度。
package node

import "ontology/key"

// Node 是跳表节点，占据第 0..Level-1 层。
// Next[i] 是第 i 层后继，Span[i] 是该指针跨过的底层元素个数。
type Node struct {
	Key   key.K
	Level int
	Next  []*Node
	Span  []int
}

// New 创建一个层高为 level 的节点。
func New(k key.K, level int) *Node {
	return &Node{Key: k, Level: level, Next: make([]*Node, level), Span: make([]int, level)}
}

// Head 创建哨兵头节点，层高为 maxLevel，不持有元素。
func Head(maxLevel int) *Node {
	return &Node{Level: maxLevel, Next: make([]*Node, maxLevel), Span: make([]int, maxLevel)}
}
