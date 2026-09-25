// Package node 提供无锁队列的链表节点：值 + 原子 next 指针，以及哨兵构造。
// 它不依赖项目内任何其他包。
package node

import "sync/atomic"

// Node 是链表节点。哨兵节点的 val 无意义，仅作为占位。
type Node struct {
	val  int
	next atomic.Pointer[Node]
}

// New 构造一个携带值 v 的普通节点。
func New(v int) *Node {
	return &Node{val: v}
}

// Sentinel 构造哨兵节点：空队列的 head、tail 都指向它。
func Sentinel() *Node {
	return &Node{}
}

// Value 返回节点携带的值（对哨兵调用是未定义行为，队列实现不会这么做）。
func (n *Node) Value() int {
	return n.val
}

// Next 原子地读取后继节点，无后继时返回 nil。
func (n *Node) Next() *Node {
	return n.next.Load()
}

// CASNext 仅当当前后继为 old 时把后继原子地换成 new，返回是否成功。
func (n *Node) CASNext(old, new *Node) bool {
	return n.next.CompareAndSwap(old, new)
}
