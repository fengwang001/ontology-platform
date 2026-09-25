// Package node 提供无锁队列的链表节点与哨兵构造，不依赖其他包。
package node

import "sync/atomic"

// Node 是链表节点：一个 int 值 + 原子 next 指针。
type Node struct {
	Val  int
	Next atomic.Pointer[Node]
}

// Sentinel 构造哨兵节点：空队列的 head、tail 都指向它，本身不承载元素。
func Sentinel() *Node { return &Node{} }
