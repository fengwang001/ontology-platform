// Package level 提供单级 FIFO 作业队列。
package level

// Queue 是 int 作业 id 的先进先出队列，零值即可用。
type Queue struct {
	items []int
	head  int
}

// Push 把 id 追加到队尾。
func (q *Queue) Push(id int) { q.items = append(q.items, id) }

// Pop 移除并返回队首 id，队列空时 panic。
func (q *Queue) Pop() int {
	id := q.items[q.head]
	q.head++
	if q.head == len(q.items) {
		q.items = q.items[:0]
		q.head = 0
	}
	return id
}

// Front 返回队首 id 但不移除，队列空时 panic。
func (q *Queue) Front() int { return q.items[q.head] }

// Len 返回队列中的元素个数。
func (q *Queue) Len() int { return len(q.items) - q.head }
