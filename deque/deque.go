// Package deque 提供无外部依赖的环形双端队列，两端入出均摊 O(1)。
package deque

// Deque 是可增长的泛型环形双端队列，按位置 0..Len()-1 从队首到队尾定位元素。
type Deque[T any] struct {
	buf   []T
	head  int
	count int
}

// New 创建一个空队列。
func New[T any]() *Deque[T] {
	return &Deque[T]{buf: make([]T, 1)}
}

// Len 返回队列长度。
func (q *Deque[T]) Len() int { return q.count }

func (q *Deque[T]) grow() {
	bigger := make([]T, len(q.buf)*2)
	for i := 0; i < q.count; i++ {
		bigger[i] = q.buf[(q.head+i)%len(q.buf)]
	}
	q.buf, q.head = bigger, 0
}

// PushBack 将元素加入队尾。
func (q *Deque[T]) PushBack(v T) {
	if q.count == len(q.buf) {
		q.grow()
	}
	q.buf[(q.head+q.count)%len(q.buf)] = v
	q.count++
}

// PopFront 移除并返回队首元素；空队列返回零值与 false。
func (q *Deque[T]) PopFront() (T, bool) {
	var zero T
	if q.count == 0 {
		return zero, false
	}
	v := q.buf[q.head]
	q.buf[q.head] = zero
	q.head = (q.head + 1) % len(q.buf)
	q.count--
	return v, true
}

// PopBack 移除并返回队尾元素；空队列返回零值与 false。
func (q *Deque[T]) PopBack() (T, bool) {
	var zero T
	if q.count == 0 {
		return zero, false
	}
	pos := (q.head + q.count - 1) % len(q.buf)
	v := q.buf[pos]
	q.buf[pos] = zero
	q.count--
	return v, true
}

// Front 返回队首元素；空队列返回零值与 false。
func (q *Deque[T]) Front() (T, bool) {
	var zero T
	if q.count == 0 {
		return zero, false
	}
	return q.buf[q.head], true
}

// At 返回从队首起第 pos 个元素（At(0) 即队首）；越界返回零值与 false。
func (q *Deque[T]) At(pos int) (T, bool) {
	var zero T
	if pos < 0 || pos >= q.count {
		return zero, false
	}
	return q.buf[(q.head+pos)%len(q.buf)], true
}
