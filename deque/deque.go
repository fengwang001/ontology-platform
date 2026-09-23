package deque

import "sync"

// Deque 是单拥有者工作窃取双端队列：拥有者在底部 Push/Pop（LIFO），
// 窃取者从顶部 StealHalf（FIFO）。固定容量环形缓冲。
type Deque[T any] struct {
	mu    sync.Mutex
	buf   []T
	head  int // 顶部（窃取端）下标
	tail  int // 底部（拥有者端）之后一位
	cap   int
	moves int64
}
func New[T any](capacity int) *Deque[T] {
	return &Deque[T]{buf: make([]T, capacity), cap: capacity}
}

// Push 由拥有者在底部放入；满了返回 false。
func (d *Deque[T]) Push(v T) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.tail-d.head >= d.cap {
		return false
	}
	d.buf[d.tail%d.cap] = v
	d.tail++
	return true
}

// Pop 由拥有者在底部取出（LIFO）。
func (d *Deque[T]) Pop() (T, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.tail == d.head {
		var z T
		return z, false
	}
	d.tail--
	v := d.buf[d.tail%d.cap]
	var z T
	d.buf[d.tail%d.cap] = z
	d.compact()
	return v, true
}

// StealHalf 由窃取者从顶部取走 ceil(n/2) 个，保持原先后顺序追加到 dst。
func (d *Deque[T]) StealHalf(dst []T) []T {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := d.tail - d.head
	k := (n + 1) / 2
	for i := 0; i < k; i++ {
		v := d.buf[d.head%d.cap]
		var z T
		d.buf[d.head%d.cap] = z
		dst = append(dst, v)
		d.head++
	}
	d.compact()
	return dst
}

// PushMany 把窃取来的批次按序放入本队列底部，返回实际放入数。
func (d *Deque[T]) PushMany(vs []T) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	k := 0
	for _, v := range vs {
		if d.tail-d.head >= d.cap {
			break
		}
		d.buf[d.tail%d.cap] = v
		d.tail++
		k++
	}
	return k
}

func (d *Deque[T]) Len() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.tail - d.head
}

// Moves 返回元素搬移次数。环形缓冲按模索引，不做整体前移；
// 仅在变空时重置下标，此时无元素需要搬移，故恒为 0。
func (d *Deque[T]) Moves() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.moves
}

// compact 仅把空队列的下标归零（不搬移任何元素）。
func (d *Deque[T]) compact() {
	if d.head == d.tail {
		d.head, d.tail = 0, 0
	}
}
