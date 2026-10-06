package ftl

// minHeap 通用最小堆，避免 container/heap 为每种键重复样板代码。
// 所有选择结构（空闲块、受害块、冷块、擦除次数极值）均为对数开销，
// 不随块总数线性增长。
type minHeap[T any] struct {
	items []T
	less  func(a, b T) bool
}

func newMinHeap[T any](less func(a, b T) bool) minHeap[T] {
	return minHeap[T]{less: less}
}

func (h *minHeap[T]) len() int { return len(h.items) }

func (h *minHeap[T]) push(x T) {
	h.items = append(h.items, x)
	for i := len(h.items) - 1; i > 0; {
		parent := (i - 1) / 2
		if !h.less(h.items[i], h.items[parent]) {
			break
		}
		h.items[i], h.items[parent] = h.items[parent], h.items[i]
		i = parent
	}
}

func (h *minHeap[T]) peek() (T, bool) {
	if len(h.items) == 0 {
		var zero T
		return zero, false
	}
	return h.items[0], true
}

func (h *minHeap[T]) pop() (T, bool) {
	var zero T
	n := len(h.items)
	if n == 0 {
		return zero, false
	}
	top := h.items[0]
	h.items[0] = h.items[n-1]
	h.items = h.items[:n-1]
	for i := 0; ; {
		child := 2*i + 1
		if child >= len(h.items) {
			break
		}
		if child+1 < len(h.items) && h.less(h.items[child+1], h.items[child]) {
			child++
		}
		if !h.less(h.items[child], h.items[i]) {
			break
		}
		h.items[i], h.items[child] = h.items[child], h.items[i]
		i = child
	}
	return top, true
}

// 空闲块选取键：擦除次数最少，并列取块号较小者。
type freeEntry struct {
	erases int
	id     int
}

// 受害块选取键：有效页数最少，并列擦除次数较少，再并列块号较小。
type victimEntry struct {
	valid  int
	erases int
	id     int
}

// 冷块选取键：擦除次数最少，并列块号较小。
type coldEntry struct {
	erases int
	id     int
}

// 擦除次数极值键（最小堆与最大堆共用，靠 less 方向区分）。
type eraseEntry struct {
	erases int
	id     int
}
