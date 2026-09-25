// Package node 定义无锁栈使用的单链表节点：一个值加一个原子 next 指针。
// 本包不依赖工程内任何其他包。
package node

import "sync/atomic"

// Node 是 Treiber 栈的单链表节点。next 一经发布不再改写，
// 但仍以原子指针承载，保证无锁读取端无数据竞争。
type Node struct {
	v    int
	next atomic.Pointer[Node]
}

// New 创建一个值为 v、后继指向 next 的节点（next 可为 nil）。
func New(v int, next *Node) *Node {
	nd := &Node{v: v}
	nd.next.Store(next)
	return nd
}

// V 返回节点保存的值。
func (n *Node) V() int { return n.v }

// Next 原子返回后继指针；栈底节点返回 nil。
func (n *Node) Next() *Node { return n.next.Load() }
