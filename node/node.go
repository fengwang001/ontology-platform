// Package node 定义确定性跳表的节点：每层后继指针与该指针的跨度。
package node

import "ontology/key"

// Level 保存一层上的后继指针与其跨度。
// 跨度 Span 恰等于 Next 指针跨过的底层数据元素个数。
type Level struct {
	Next *Node
	Span int
}

// Node 是跳表中的一个节点（含头、尾哨兵与数据节点）。
type Node struct {
	Key   key.Key
	Level []Level
}

// New 创建一个给定键、给定层高的节点，所有后继先指向 nil、跨度为 0。
func New(k key.Key, height int) *Node {
	return &Node{Key: k, Level: make([]Level, height)}
}

// Height 返回节点所在的最高层号（等于 Level 切片长度）。
func (n *Node) Height() int { return len(n.Level) }

// At 取第 level 层（1 基）的链路信息。
func (n *Node) At(level int) *Level { return &n.Level[level-1] }

// Equal 逐字段比较两个节点的键、层高与每层后继键、跨度，供结构等价性核验。
func (n *Node) Equal(o *Node) bool {
	if n == nil || o == nil || n.Key != o.Key || len(n.Level) != len(o.Level) {
		return false
	}
	for i := range n.Level {
		a, b := n.Level[i], o.Level[i]
		if a.Span != b.Span {
			return false
		}
		if (a.Next == nil) != (b.Next == nil) {
			return false
		}
		if a.Next != nil && a.Next.Key != b.Next.Key {
			return false
		}
	}
	return true
}
