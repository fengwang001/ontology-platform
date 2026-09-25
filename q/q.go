// Package q 实现 Michael–Scott 风格的无锁 MPMC FIFO 队列：
// 链表 + CAS + 哨兵节点，Len 用原子计数 O(1) 得到。只依赖 node 包。
package q

import (
	"errors"
	"sync/atomic"

	"ontology/node"
)

// 可判定的哨兵错误，三者互不相同。
var (
	ErrBadMaxLen = errors.New("q: maxLen must be >= 1")
	ErrFull      = errors.New("q: queue is full")
	ErrClosed    = errors.New("q: queue is closed")
)

// Q 是无界（maxLen 仅为报满的软上限）无锁队列，元素为 int。
type Q struct {
	head   atomic.Pointer[node.Node]
	tail   atomic.Pointer[node.Node]
	count  atomic.Int64 // 已入队数 - 已出队数，恒在 [0, max]
	max    int64
	closed atomic.Bool
	// lastVisit 记录最近一次 Dequeue 访问过的节点个数。
	// 非导出字段，不出现在任何公开接口里。
	lastVisit atomic.Int64
}

// New 构造空队列：head、tail 都指向哨兵节点。maxLen < 1 报 ErrBadMaxLen。
func New(maxLen int) (*Q, error) {
	if maxLen < 1 {
		return nil, ErrBadMaxLen
	}
	s := node.Sentinel()
	qq := &Q{max: int64(maxLen)}
	qq.head.Store(s)
	qq.tail.Store(s)
	return qq, nil
}

// Enqueue 追加到队尾，只碰尾节点，O(1)。
// 满报 ErrFull、已关闭报 ErrClosed，两者都不改任何状态。
func (q *Q) Enqueue(v int) error {
	if q.closed.Load() {
		return ErrClosed
	}
	// 先 CAS 预约名额：保证 count 恒 <= max，失败即满，整体不留痕。
	for {
		c := q.count.Load()
		if c >= q.max {
			return ErrFull
		}
		if q.count.CompareAndSwap(c, c+1) {
			break
		}
	}
	n := node.New(v)
	for {
		t := q.tail.Load()
		next := t.Next()
		if t != q.tail.Load() { // tail 已变，重读
			continue
		}
		if next == nil {
			if t.CASNext(nil, n) { // 链接成功即入队生效
				q.tail.CompareAndSwap(t, n) // 推进 tail；失败由他人帮忙
				return nil
			}
		} else {
			q.tail.CompareAndSwap(t, next) // tail 落后，帮忙推进
		}
	}
}

// Dequeue 取出队首，只碰头节点，O(1)；空时返回 (0, false) 且不改状态。
// Close 之后剩余元素照常排空，排空后永久返回 (0, false)。
func (q *Q) Dequeue() (int, bool) {
	visited := 0
	for {
		visited++
		h := q.head.Load()
		t := q.tail.Load()
		next := h.Next()
		if h != q.head.Load() { // head 已变，重试
			continue
		}
		if next == nil { // 空（含 Close 后排空）：head 即哨兵且无后继
			q.lastVisit.Store(int64(visited))
			return 0, false
		}
		if h == t { // tail 落后于 head，帮忙推进后重试
			q.tail.CompareAndSwap(t, next)
			continue
		}
		v := next.Value() // 值在 head 的后继上；摘除后旧 head 成为新哨兵
		if q.head.CompareAndSwap(h, next) {
			q.count.Add(-1)
			q.lastVisit.Store(int64(visited))
			return v, true
		}
	}
}

// Len 等于已入队数减已出队数，原子读取，O(1)，不遍历链表。
func (q *Q) Len() int {
	return int(q.count.Load())
}

// Close 置关闭标志（幂等，重复调用仍返回 nil）。
// 关闭后 Enqueue 恒报 ErrClosed，Dequeue 继续排空剩余元素。
func (q *Q) Close() error {
	q.closed.Store(true)
	return nil
}
