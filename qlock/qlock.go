// Package qlock 实现 MCS 队列自旋锁：原子 swap tail 入队、本地自旋、
// 「CAS tail 确认 + 自旋等后继」的交接、关闭。
package qlock

import (
	"errors"
	"runtime"
	"sync/atomic"

	"ontology/mcsnode"
)

// 三类可判定哨兵错误，互不相同。
var (
	ErrClosed   = errors.New("qlock: closed")
	ErrNilNode  = errors.New("qlock: nil node")
	ErrNotOwner = errors.New("qlock: not the current holder")
)

// Lock 是 MCS 队列锁。holder 即队首（当前持有者的节点）。
type Lock struct {
	tail    atomic.Pointer[mcsnode.Node]
	holder  atomic.Pointer[mcsnode.Node]
	closed  atomic.Bool
	visited atomic.Int64 // 最近一次 Acquire/Release 访问的节点数（非导出）
}

// New 返回一把打开的锁。
func New() *Lock { return &Lock{} }

// Acquire 入队并自旋直到获锁。已关闭返回 ErrClosed。
func (l *Lock) Acquire() (*mcsnode.Node, error) {
	if l.closed.Load() {
		return nil, ErrClosed
	}
	n := mcsnode.New()
	pred := l.tail.Swap(n)
	if pred == nil { // 无前驱，直接获锁
		l.visited.Store(1)
		l.holder.Store(n)
		return n, nil
	}
	l.visited.Store(2) // 自己 + 前驱
	pred.SetNext(n)
	for n.Locked() { // 本地自旋：只读自己的 locked
		runtime.Gosched()
	}
	return n, nil
}

// Release 释放 node 并把锁交给后继。node 必须是当前持有者。
func (l *Lock) Release(n *mcsnode.Node) error {
	if n == nil {
		return ErrNilNode
	}
	if !l.holder.CompareAndSwap(n, nil) { // 校验并占位，失败即非持有者
		return ErrNotOwner
	}
	l.visited.Store(1)
	if succ := n.Next(); succ != nil { // 交接：清后继的 locked
		l.visited.Store(2)
		l.holder.Store(succ)
		succ.Unlock()
		return nil
	}
	if l.tail.CompareAndSwap(n, nil) { // 确认无后继
		return nil
	}
	l.visited.Store(2) // 有人已 swap tail，自旋等它链 next
	for {
		if succ := n.Next(); succ != nil {
			l.holder.Store(succ)
			succ.Unlock()
			return nil
		}
		runtime.Gosched()
	}
}

// Close 置关闭标志，之后 Acquire 持续报 ErrClosed。
func (l *Lock) Close() error {
	l.closed.Store(true)
	return nil
}

// Snapshot 返回从队首（持有者）到 tail 的节点序列，供测试与自检。
func (l *Lock) Snapshot() []*mcsnode.Node {
	var out []*mcsnode.Node
	for n := l.holder.Load(); n != nil; n = n.Next() {
		out = append(out, n)
	}
	return out
}
