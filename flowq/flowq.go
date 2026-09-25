// Package flowq 维护单个 flow 的 FIFO 数据块队列，并记录每块字节数。
// 它不依赖本工程中的其他包。
package flowq

// Queue 是单个 flow 的块队列。零值不可直接使用，请用 New 构造。
type Queue struct {
	blocks []int
	head   int
	bytes  int
}

// New 返回一个空队列。
func New() *Queue {
	return &Queue{blocks: make([]int, 0, 8)}
}

// Push 在队尾放入一块大小为 size 字节的数据块。
func (q *Queue) Push(size int) {
	if q.head == len(q.blocks) {
		q.blocks = q.blocks[:0]
		q.head = 0
	}
	q.blocks = append(q.blocks, size)
	q.bytes += size
}

// Pop 移除并返回队首块；队列为空时返回 (0, false)。
func (q *Queue) Pop() (int, bool) {
	if q.head == len(q.blocks) {
		return 0, false
	}
	size := q.blocks[q.head]
	q.head++
	q.bytes -= size
	if q.head == len(q.blocks) {
		q.blocks = q.blocks[:0]
		q.head = 0
	}
	return size, true
}

// Head 返回队首块大小而不移除；队列为空时返回 (0, false)。
func (q *Queue) Head() (int, bool) {
	if q.head == len(q.blocks) {
		return 0, false
	}
	return q.blocks[q.head], true
}

// Len 返回队列中的块数。
func (q *Queue) Len() int { return len(q.blocks) - q.head }

// Bytes 返回队列中所有块的字节总数。
func (q *Queue) Bytes() int { return q.bytes }
