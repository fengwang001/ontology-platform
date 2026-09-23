// Package deque 是单拥有者工作窃取双端队列：拥有者在 bottom 端
// Push/Pop（LIFO），窃取者在 top 端 StealHalf（FIFO）。
package deque

import "sync"

const initCap = 8

// Deque 环形缓冲。bottom 为独占写端，top 为窃取端。
type Deque[T any] struct {
	mu    sync.Mutex
	top   int
	bot   int
	buf   []T
	max   int
	Moves int64 // 非导出语义的计数器（测试读取）：扩容搬移元素数
}

func New[T any](max int) *Deque[T] {
	c := initCap
	if max < c {
		c = max
	}
	return &Deque[T]{buf: make([]T, c), max: max}
}

func (q *Deque[T]) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.bot - q.top
}

func (q *Deque[T]) Full() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.bot-q.top >= q.max
}

// Push 拥有者在 bottom 端放入。满时返回 false（不扩容到超过 max）。
func (q *Deque[T]) Push(v T) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.bot-q.top >= q.max {
		return false
	}
	if q.bot-q.top == len(q.buf) {
		q.grow()
	}
	q.buf[q.bot&(len(q.buf)-1)] = v
	q.bot++
	return true
}

// Pop 拥有者取 bottom（LIFO）。
func (q *Deque[T]) Pop() (T, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var zero T
	if q.bot == q.top {
		return zero, false
	}
	q.bot--
	v := q.buf[q.bot&(len(q.buf)-1)]
	q.buf[q.bot&(len(q.buf)-1)] = zero
	if q.bot == q.top { // 与窃取者争最后一个：拥有者拿走，窃取端归零
		q.bot, q.top = 0, 0
	}
	return v, true
}

// StealHalf 窃取者从 top 端取：n==1 偷 1；n>=2 偷 floor(n/2)。
// 返回元素保持受害者队列中的先后顺序。
func (q *Deque[T]) StealHalf() []T {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := q.bot - q.top
	if n == 0 {
		return nil
	}
	k := n / 2
	if n == 1 {
		k = 1
	}
	out := make([]T, k)
	mask := len(q.buf) - 1
	for i := 0; i < k; i++ {
		out[i] = q.buf[(q.top+i)&mask]
		var zero T
		q.buf[(q.top+i)&mask] = zero
	}
	q.top += k
	if q.top == q.bot {
		q.bot, q.top = 0, 0
	}
	return out
}

// PrependMany 把元素按原顺序放到 top 端（用于装入偷来的任务，保持先后）。
func (q *Deque[T]) PrependMany(vals []T) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.bot-q.top+len(vals) > q.max {
		return false
	}
	for q.bot-q.top+len(vals) > len(q.buf) {
		q.grow()
	}
	mask := len(q.buf) - 1
	for i := len(vals) - 1; i >= 0; i-- {
		q.top--
		q.buf[q.top&mask] = vals[i]
	}
	return true
}

// PopHalfBottom 拥有者在满时把 bottom 一半（较新的一半）取出转移。
func (q *Deque[T]) PopHalfBottom() []T {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := q.bot - q.top
	if n < 2 {
		return nil
	}
	k := n / 2
	start := q.bot - k
	out := make([]T, k)
	mask := len(q.buf) - 1
	for i := 0; i < k; i++ {
		out[i] = q.buf[(start+i)&mask]
		var zero T
		q.buf[(start+i)&mask] = zero
	}
	q.bot = start
	return out
}

func (q *Deque[T]) grow() {
	old := q.buf
	nb := make([]T, len(old)*2)
	n := q.bot - q.top
	mask := len(nb) - 1
	for i := 0; i < n; i++ {
		nb[i&mask] = old[(q.top+i)&(len(old)-1)]
	}
	q.Moves += int64(n)
	q.buf = nb
	q.top, q.bot = 0, n
}
