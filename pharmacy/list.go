package pharmacy

// boNode 是欠药队列节点，侵入式双向链表。
// unlink 时保留 prev/next 指针，使回滚可以原样恢复链接。
type boNode struct {
	line       *Line
	prev, next *boNode
}

// boQueue 以头尾哨兵组织，front 为 head.next（不等于尾哨兵时非空）。
type boQueue struct {
	head, tail *boNode
	length     int
}

func (q *boQueue) init() {
	q.head = &boNode{}
	q.tail = &boNode{}
	q.head.next = q.tail
	q.tail.prev = q.head
}

func (q *boQueue) front() *boNode {
	if q.head.next == q.tail {
		return nil
	}
	return q.head.next
}

func (q *boQueue) pushBack(n *boNode) {
	last := q.tail.prev
	last.next = n
	n.prev = last
	n.next = q.tail
	q.tail.prev = n
	q.length++
}

// unlink 摘除节点但保留其 prev/next，供回滚恢复。
func (q *boQueue) unlink(n *boNode) {
	n.prev.next = n.next
	n.next.prev = n.prev
	q.length--
}

// relink 是 unlink 的逆操作，要求 n.prev/n.next 仍为摘除时的邻居。
func (q *boQueue) relink(n *boNode) {
	n.prev.next = n
	n.next.prev = n
	q.length++
}
