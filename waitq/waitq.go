// Package waitq 是 O(1) 取消的等待者 FIFO 队列。
package waitq

import "context"

// Waiter 是队列节点；指针内嵌于节点，摘除只触达 prev/next 两个节点。
type Waiter struct {
	Ctx     context.Context
	N       int64
	ready   chan struct{}
	prev    *Waiter
	next    *Waiter
	queued  bool
	Granted bool
}

// NewWaiter 创建一个权重 n 的等待者。
func NewWaiter(ctx context.Context, n int64) *Waiter {
	return &Waiter{Ctx: ctx, N: n, ready: make(chan struct{})}
}

// Ready 在等待者被满足时关闭，供等待方 select。
func (w *Waiter) Ready() <-chan struct{} { return w.ready }

// Queue 是双向 FIFO 链表；并发由外层 sem 的互斥锁保护。
type Queue struct {
	head *Waiter
	tail *Waiter
	n    int

	// cancelChecks 统计自上次 ResetCounters 起取消操作触达的节点数。
	cancelChecks int
}

// Len 返回等待者数量。
func (q *Queue) Len() int { return q.n }

// Head 返回队首，队列为空时为 nil。
func (q *Queue) Head() *Waiter { return q.head }

// Push 把等待者追加到队尾。
func (q *Queue) Push(w *Waiter) {
	w.prev, w.next, w.queued = q.tail, nil, true
	if q.tail != nil {
		q.tail.next = w
	} else {
		q.head = w
	}
	q.tail = w
	q.n++
}

// Remove 在 O(1) 内摘除节点；触达的节点数计入 cancelChecks（至多 2）。
func (q *Queue) Remove(w *Waiter) bool {
	if w == nil || !w.queued {
		return false
	}
	q.cancelChecks += 2 // 重新链接 prev 与 next 两个邻居
	if w.prev != nil {
		w.prev.next = w.next
	} else {
		q.head = w.next
	}
	if w.next != nil {
		w.next.prev = w.prev
	} else {
		q.tail = w.prev
	}
	w.prev, w.next, w.queued = nil, nil, false
	q.n--
	return true
}

// Grant 标记等待者已拿到额度并关闭唤醒通道。
func (w *Waiter) Grant() {
	w.Granted = true
	close(w.ready)
}

// Pop 摘除并返回队首；这是被满足路径的正常出队，不计取消统计。
func (q *Queue) Pop() *Waiter {
	w := q.head
	if w == nil {
		return nil
	}
	q.head = w.next
	if q.head != nil {
		q.head.prev = nil
	} else {
		q.tail = nil
	}
	w.prev, w.next, w.queued = nil, nil, false
	q.n--
	return w
}

// CancelChecks 返回取消计数器当前值；ResetCounters 清零。
func (q *Queue) CancelChecks() int { return q.cancelChecks }

// ResetCounters 清零非导出统计计数器。
func (q *Queue) ResetCounters() { q.cancelChecks = 0 }
