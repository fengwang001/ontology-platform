// Package mcsnode 定义 MCS 队列锁的节点：本地自旋标志 + 原子后继指针。
package mcsnode

import "sync/atomic"

// Node 是队列中的一个等待节点。等待者只在自己的 locked 上自旋。
type Node struct {
	locked atomic.Bool
	next   atomic.Pointer[Node]
}

// New 创建一个处于「未获锁」状态的节点。
func New() *Node {
	n := &Node{}
	n.locked.Store(true)
	return n
}

// Locked 读自己的自旋标志（本地自旋唯一读取点）。
func (n *Node) Locked() bool { return n.locked.Load() }

// Unlock 由前驱在交接时调用，清后继的 locked。
func (n *Node) Unlock() { n.locked.Store(false) }

// Next 读后继。
func (n *Node) Next() *Node { return n.next.Load() }

// SetNext 由前驱在入队时链上后继。
func (n *Node) SetNext(m *Node) { n.next.Store(m) }
