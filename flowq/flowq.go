package flowq

// Queue stores block sizes in FIFO order.
type Queue struct {
	items []int
	bytes int
}

func New() *Queue {
	return &Queue{items: make([]int, 0)}
}

func (q *Queue) Push(size int) {
	q.items = append(q.items, size)
	q.bytes += size
}

func (q *Queue) Pop() (int, bool) {
	if len(q.items) == 0 {
		return 0, false
	}
	size := q.items[0]
	q.items = q.items[1:]
	q.bytes -= size
	return size, true
}

func (q *Queue) Front() (int, bool) {
	if len(q.items) == 0 {
		return 0, false
	}
	return q.items[0], true
}

func (q *Queue) Len() int { return len(q.items) }

func (q *Queue) Bytes() int { return q.bytes }
