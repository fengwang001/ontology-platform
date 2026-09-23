// Package heapq 提供带非导出比较计数器的泛型最小堆，不依赖其他包。
package heapq

// Item 是堆中元素的句柄，可用于 O(log n) 删除。
type Item[T any] struct {
	Value T
	idx   int
	alive bool
}

// Heap 是最小堆；less 返回 true 表示 a 应排在 b 前面。
type Heap[T any] struct {
	less  func(a, b T) bool
	items []*Item[T]
	cmps  uint64
}

// New 创建以 less 为序的空堆。
func New[T any](less func(a, b T) bool) *Heap[T] {
	return &Heap[T]{less: less}
}

// Len 返回堆中元素个数。
func (h *Heap[T]) Len() int { return len(h.items) }

// CmpCount 返回自上次 ResetCmpCount 以来 less 被调用的次数。
func (h *Heap[T]) CmpCount() uint64 { return h.cmps }

// ResetCmpCount 将比较计数器清零。
func (h *Heap[T]) ResetCmpCount() { h.cmps = 0 }

func (h *Heap[T]) cmp(a, b *Item[T]) bool {
	h.cmps++
	return h.less(a.Value, b.Value)
}

// Push 插入一个元素并返回其句柄。
func (h *Heap[T]) Push(v T) *Item[T] {
	it := &Item[T]{Value: v, idx: len(h.items), alive: true}
	h.items = append(h.items, it)
	h.siftUp(it.idx)
	return it
}

// Pop 移除并返回堆顶元素。
func (h *Heap[T]) Pop() (T, bool) {
	var zero T
	if len(h.items) == 0 {
		return zero, false
	}
	root := h.items[0]
	h.remove(root)
	return root.Value, true
}

// Remove 删除指定句柄对应的元素；重复删除无副作用。
func (h *Heap[T]) Remove(it *Item[T]) {
	if it == nil || !it.alive {
		return
	}
	h.remove(it)
}

func (h *Heap[T]) remove(it *Item[T]) {
	last := h.items[len(h.items)-1]
	h.items = h.items[:len(h.items)-1]
	it.alive = false
	if it == last {
		return
	}
	h.items[it.idx] = last
	last.idx = it.idx
	if !h.siftUp(last.idx) {
		h.siftDown(last.idx)
	}
}

func (h *Heap[T]) siftUp(i int) bool {
	moved := false
	for i > 0 {
		p := (i - 1) / 2
		if !h.cmp(h.items[i], h.items[p]) {
			break
		}
		h.swap(i, p)
		i = p
		moved = true
	}
	return moved
}

func (h *Heap[T]) siftDown(i int) {
	n := len(h.items)
	for {
		l := 2*i + 1
		if l >= n {
			return
		}
		small := l
		if r := l + 1; r < n && h.cmp(h.items[r], h.items[l]) {
			small = r
		}
		if !h.cmp(h.items[small], h.items[i]) {
			return
		}
		h.swap(i, small)
		i = small
	}
}

func (h *Heap[T]) swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
	h.items[i].idx = i
	h.items[j].idx = j
}
