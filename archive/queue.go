package archive

// resvEntry 预约队列节点，按被接受的预约提交顺序排列。
type resvEntry struct {
	seq      int64
	borrower *borrower
	prev     *resvEntry
	next     *resvEntry
}

// resvQueue 为双向链表队列。节点只在被分配、期满放弃或取消时移除，
// 因此扫描开销只取决于当前存活节点，与历史预约总数无关。
type resvQueue struct {
	head *resvEntry
	tail *resvEntry
	len  int
}

func (q *resvQueue) pushBack(e *resvEntry) {
	e.prev = q.tail
	e.next = nil
	if q.tail != nil {
		q.tail.next = e
	} else {
		q.head = e
	}
	q.tail = e
	q.len++
}

func (q *resvQueue) remove(e *resvEntry) {
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
	e.prev = nil
	e.next = nil
	q.len--
}

// insertAfter 将 e 插入到 prev 之后；prev 为 nil 时插入队首。
// 仅用于被拒绝操作的期满回滚，恢复节点原序位。
func (q *resvQueue) insertAfter(prev *resvEntry, e *resvEntry) {
	e.prev = prev
	if prev != nil {
		e.next = prev.next
		prev.next = e
	} else {
		e.next = q.head
		q.head = e
	}
	if e.next != nil {
		e.next.prev = e
	} else {
		q.tail = e
	}
	q.len++
}

func (q *resvQueue) clear() {
	q.head = nil
	q.tail = nil
	q.len = 0
}

func (q *resvQueue) contains(b *borrower) bool {
	for e := q.head; e != nil; e = e.next {
		if e.borrower == b {
			return true
		}
	}
	return false
}

func (q *resvQueue) ids() []string {
	out := make([]string, 0, q.len)
	for e := q.head; e != nil; e = e.next {
		out = append(out, e.borrower.id)
	}
	return out
}
