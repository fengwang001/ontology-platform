package repair

type priorityQueue struct {
	tickets []*ticket
	index   map[int64]int
}

func newPriorityQueue() *priorityQueue {
	return &priorityQueue{index: make(map[int64]int)}
}

func (q *priorityQueue) push(t *ticket) {
	pos := len(q.tickets)
	q.tickets = append(q.tickets, t)
	q.index[t.id] = pos
	q.siftUp(pos)
}

func (q *priorityQueue) peek() *ticket {
	if len(q.tickets) == 0 {
		return nil
	}
	return q.tickets[0]
}

func (q *priorityQueue) pop() *ticket {
	if len(q.tickets) == 0 {
		return nil
	}
	t := q.tickets[0]
	q.removeAt(0)
	return t
}

func (q *priorityQueue) popAt(pos int) *ticket {
	t := q.tickets[pos]
	q.removeAt(pos)
	return t
}

func (q *priorityQueue) remove(t *ticket) {
	if pos, ok := q.index[t.id]; ok {
		q.removeAt(pos)
	}
}

func (q *priorityQueue) len() int {
	return len(q.tickets)
}

func (q *priorityQueue) siftUp(pos int) {
	for pos > 0 {
		parent := (pos - 1) / 2
		if !q.less(pos, parent) {
			return
		}
		q.swap(pos, parent)
		pos = parent
	}
}

func (q *priorityQueue) siftDown(pos int) {
	for {
		left := pos*2 + 1
		if left >= len(q.tickets) {
			return
		}
		best := left
		right := left + 1
		if right < len(q.tickets) && q.less(right, left) {
			best = right
		}
		if !q.less(best, pos) {
			return
		}
		q.swap(pos, best)
		pos = best
	}
}

func (q *priorityQueue) removeAt(pos int) {
	t := q.tickets[pos]
	delete(q.index, t.id)
	last := len(q.tickets) - 1
	if pos == last {
		q.tickets = q.tickets[:last]
		return
	}
	q.tickets[pos] = q.tickets[last]
	q.index[q.tickets[pos].id] = pos
	q.tickets = q.tickets[:last]
	q.siftDown(pos)
	q.siftUp(pos)
}

func (q *priorityQueue) swap(a, b int) {
	q.tickets[a], q.tickets[b] = q.tickets[b], q.tickets[a]
	q.index[q.tickets[a].id] = a
	q.index[q.tickets[b].id] = b
}

func (q *priorityQueue) less(a, b int) bool {
	left := q.tickets[a]
	right := q.tickets[b]
	if left.level != right.level {
		return left.level > right.level
	}
	if left.submittedAt != right.submittedAt {
		return left.submittedAt < right.submittedAt
	}
	return left.id < right.id
}
