// Package flowq 是单个 flow 的 FIFO 数据块队列，记录每块字节数。
package flowq

// Queue 按入队顺序保存块大小（字节）。
type Queue struct {
	buf   []int
	head  int
	bytes int
}

// Len 返回排队块数。
func (q *Queue) Len() int { return len(q.buf) - q.head }

// Bytes 返回排队总字节数。
func (q *Queue) Bytes() int { return q.bytes }

// Front 返回队首块大小；空队列 ok=false。
func (q *Queue) Front() (size int, ok bool) {
	if q.Len() == 0 {
		return 0, false
	}
	return q.buf[q.head], true
}

// Push 把 size 字节的块追加到队尾。
func (q *Queue) Push(size int) {
	q.buf = append(q.buf, size)
	q.bytes += size
}

// Pop 移除并返回队首块大小；空队列 ok=false。
func (q *Queue) Pop() (size int, ok bool) {
	if q.Len() == 0 {
		return 0, false
	}
	size = q.buf[q.head]
	q.head++
	q.bytes -= size
	if q.head == len(q.buf) {
		q.buf = q.buf[:0]
		q.head = 0
	}
	return size, true
}
