package flowq

type Queue struct {
	items     []int
	head, len int
	bytes     int
}

func New() *Queue { return &Queue{} }

func (q *Queue) Enqueue(size int) {
	if q.len == len(q.items) {
		items := make([]int, len(q.items)*2+1)
		for i := 0; i < q.len; i++ {
			items[i] = q.items[(q.head+i)%len(q.items)]
		}
		q.items, q.head = items, 0
	}
	i := (q.head + q.len) % cap(q.items)
	q.items[i], q.len, q.bytes = size, q.len+1, q.bytes+size
}

func (q *Queue) Dequeue() (int, bool) {
	if q.len == 0 {
		return 0, false
	}
	size := q.items[q.head]
	q.items[q.head], q.head, q.len, q.bytes = 0, (q.head+1)%cap(q.items), q.len-1, q.bytes-size
	if q.len == 0 {
		q.head = 0
	}
	return size, true
}

func (q *Queue) Peek() (int, bool) {
	if q.len == 0 {
		return 0, false
	}
	return q.items[q.head], true
}

func (q *Queue) Len() int   { return q.len }
func (q *Queue) Bytes() int { return q.bytes }
