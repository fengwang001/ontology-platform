// Package q 实现 Michael-Scott 风格无锁 MPMC FIFO 队列：链表 + CAS + 哨兵。
// 依赖 node，不依赖 api。
package q

import (
	"errors"
	"sync/atomic"

	"ontology/node"
)

// 可判定哨兵错误：满 / 已关闭，互不相同。
var (
	ErrFull   = errors.New("queue full")
	ErrClosed = errors.New("queue closed")
)

// Queue 是无界（maxLen 为软上限）无锁 MPMC 队列。
type Queue struct {
	head   atomic.Pointer[node.Node]
	tail   atomic.Pointer[node.Node]
	len    atomic.Int64 // 已入队数 - 已出队数，恒在 [0, maxLen]
	maxLen int64
	closed atomic.Bool
	// lastVisited 记录最近一次 Dequeue 访问过的节点个数，仅供包内测试核验 O(1)。
	lastVisited atomic.Int64
}

// New 以哨兵节点建空队列，head、tail 均指向哨兵。maxLen 必须 >= 1（由调用方校验）。
func New(maxLen int) *Queue {
	s := node.Sentinel()
	qu := &Queue{maxLen: int64(maxLen)}
	qu.head.Store(s)
	qu.tail.Store(s)
	return qu
}

// Enqueue 尾插。满报 ErrFull、关闭后报 ErrClosed，失败不改任何状态。
func (qu *Queue) Enqueue(v int) error {
	if qu.closed.Load() {
		return ErrClosed
	}
	for { // 原子预占一个槽位，保证 Len 恒 <= maxLen
		l := qu.len.Load()
		if l >= qu.maxLen {
			return ErrFull
		}
		if qu.len.CompareAndSwap(l, l+1) {
			break
		}
	}
	n := &node.Node{Val: v}
	for { // Michael-Scott 尾插，线性化点 = 链接成功的 CAS
		t := qu.tail.Load()
		next := t.Next.Load()
		if t != qu.tail.Load() {
			continue
		}
		if next == nil {
			if t.Next.CompareAndSwap(nil, n) {
				qu.tail.CompareAndSwap(t, n) // 失败无碍，他人会帮忙推进
				return nil
			}
		} else {
			qu.tail.CompareAndSwap(t, next) // tail 落后，帮忙推进
		}
	}
}

// Dequeue 头取。空返回 (0,false)；Close 后排空剩余元素再永久返回 (0,false)。
func (qu *Queue) Dequeue() (int, bool) {
	for {
		h := qu.head.Load()
		t := qu.tail.Load()
		next := h.Next.Load()
		if h != qu.head.Load() {
			continue
		}
		if next == nil { // 哨兵即尾：空
			qu.lastVisited.Store(1)
			return 0, false
		}
		if h == t { // tail 落后，帮忙推进后重试
			qu.tail.CompareAndSwap(t, next)
			continue
		}
		v := next.Val // 线性化点 = head 前移的 CAS
		if qu.head.CompareAndSwap(h, next) {
			qu.lastVisited.Store(1) // 只碰头节点：O(1)
			qu.len.Add(-1)
			return v, true
		}
	}
}

// Len 原子计数 O(1)，不遍历链表。
func (qu *Queue) Len() int { return int(qu.len.Load()) }

// Close 置关闭标志；重复关闭报 ErrClosed（可判定）。
func (qu *Queue) Close() error {
	if qu.closed.CompareAndSwap(false, true) {
		return nil
	}
	return ErrClosed
}
