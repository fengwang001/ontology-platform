package archive

// qEntry 是预约队列中的一个节点。队列采用双向链表，取消即 O(1) 物理摘除，
// 归还时的分配扫描只访问当前仍在队中的存活节点，开销不随历史预约总数增长。
type qEntry struct {
	userID string
	prev   *qEntry
	next   *qEntry
}

// reservationQueue 是保持提交顺序的预约队列。
type reservationQueue struct {
	head *qEntry
	tail *qEntry
	size int
}

func (q *reservationQueue) enqueue(userID string) *qEntry {
	e := &qEntry{userID: userID}
	if q.tail == nil {
		q.head, q.tail = e, e
	} else {
		e.prev = q.tail
		q.tail.next = e
		q.tail = e
	}
	q.size++
	return e
}

func (q *reservationQueue) remove(e *qEntry) {
	if e == nil {
		return
	}
	if e.prev != nil {
		e.prev.next = e.next
	} else {
		q.head = e.next
	}
	if e.next != nil {
		e.next.prev = e.prev
	} else {
		q.tail = e.prev
	}
	e.prev, e.next = nil, nil
	q.size--
}

func (q *reservationQueue) len() int { return q.size }

func (q *reservationQueue) users() []string {
	out := make([]string, 0, q.size)
	for e := q.head; e != nil; e = e.next {
		out = append(out, e.userID)
	}
	return out
}

func (q *reservationQueue) contains(userID string) bool {
	for e := q.head; e != nil; e = e.next {
		if e.userID == userID {
			return true
		}
	}
	return false
}

func (q *reservationQueue) clear() {
	q.head, q.tail, q.size = nil, nil, 0
}
