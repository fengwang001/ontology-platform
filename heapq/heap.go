// Package heapq 是带比较计数的最小二叉堆。
package heapq

// Heap 为最小堆；less 返回 true 表示 a 应排在 b 前面。
// swapped 在每次元素交换后回调新下标，便于外部维护索引。
type Heap[T any] struct {
	data    []T
	less    func(a, b T) bool
	swapped func(v T, i int)
	cmps    int64
}

// New 构造空堆。
func New[T any](less func(a, b T) bool, swapped func(v T, i int)) *Heap[T] {
	return &Heap[T]{less: less, swapped: swapped}
}

// Len 返回元素数。
func (h *Heap[T]) Len() int { return len(h.data) }

// Comparisons 返回建堆以来的比较计数；每次元素比较恰好计 1。
func (h *Heap[T]) Comparisons() int64 { return h.cmps }

// ResetComparisons 清零比较计数。
func (h *Heap[T]) ResetComparisons() { h.cmps = 0 }

// Push 插入一个元素。
func (h *Heap[T]) Push(v T) {
	h.data = append(h.data, v)
	i := len(h.data) - 1
	if h.swapped != nil {
		h.swapped(v, i)
	}
	h.siftUp(i)
}

// Pop 移除并返回堆顶；空堆返回零值与 false。
func (h *Heap[T]) Pop() (T, bool) {
	var zero T
	n := len(h.data)
	if n == 0 {
		return zero, false
	}
	top := h.data[0]
	last := h.data[n-1]
	h.data = h.data[:n-1]
	if n > 1 {
		h.data[0] = last
		if h.swapped != nil {
			h.swapped(last, 0)
		}
		h.siftDown(0)
	}
	return top, true
}

// RemoveAt 移除下标 i 处元素，返回它与是否成功。
func (h *Heap[T]) RemoveAt(i int) (T, bool) {
	var zero T
	n := len(h.data)
	if i < 0 || i >= n {
		return zero, false
	}
	out := h.data[i]
	last := h.data[n-1]
	h.data = h.data[:n-1]
	if i < n-1 {
		h.data[i] = last
		if h.swapped != nil {
			h.swapped(last, i)
		}
		h.siftUp(i)
		h.siftDown(i)
	}
	return out, true
}

func (h *Heap[T]) cmp(a, b T) bool {
	h.cmps++
	return h.less(a, b)
}

func (h *Heap[T]) swap(i, j int) {
	h.data[i], h.data[j] = h.data[j], h.data[i]
	if h.swapped != nil {
		h.swapped(h.data[i], i)
		h.swapped(h.data[j], j)
	}
}

func (h *Heap[T]) siftUp(i int) {
	for i > 0 {
		p := (i - 1) / 2
		if !h.cmp(h.data[i], h.data[p]) {
			return
		}
		h.swap(i, p)
		i = p
	}
}

func (h *Heap[T]) siftDown(i int) {
	n := len(h.data)
	for {
		l, r, best := 2*i+1, 2*i+2, i
		if l < n && h.cmp(h.data[l], h.data[best]) {
			best = l
		}
		if r < n && h.cmp(h.data[r], h.data[best]) {
			best = r
		}
		if best == i {
			return
		}
		h.swap(i, best)
		i = best
	}
}
