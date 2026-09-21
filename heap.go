package ontology

// heapItem 是堆中元素：某条流的某个元素。
type heapItem struct {
	value  float64
	stream int // 来源流下标
	index  int // 元素在来源流中的下标
}

// minHeap 是按 value 排序的小顶堆，大小不超过流的条数。
// 它精确统计上浮/下沉过程中进行的元素比较次数。
type minHeap struct {
	items       []heapItem
	comparisons *int64
}

func newMinHeap(capacity int, comparisons *int64) *minHeap {
	return &minHeap{
		items:       make([]heapItem, 0, capacity),
		comparisons: comparisons,
	}
}

func (h *minHeap) len() int { return len(h.items) }

func (h *minHeap) less(i, j int) bool {
	*h.comparisons++
	return h.items[i].value < h.items[j].value
}

// push 把一个元素放入堆并上浮。比较次数至多 ceil(log2(n+1))。
func (h *minHeap) push(it heapItem) {
	h.items = append(h.items, it)
	child := len(h.items) - 1
	for child > 0 {
		parent := (child - 1) / 2
		if !h.less(child, parent) {
			break
		}
		h.items[child], h.items[parent] = h.items[parent], h.items[child]
		child = parent
	}
}

// pop 弹出堆顶最小元素并下沉新堆顶。
// 每层至多 2 次比较，总比较次数至多 2*ceil(log2(n))。
func (h *minHeap) pop() heapItem {
	n := len(h.items) - 1
	h.items[0], h.items[n] = h.items[n], h.items[0]
	top := h.items[n]
	h.items = h.items[:n]

	parent := 0
	for {
		left := 2*parent + 1
		if left >= n {
			break
		}
		smallest := left
		if right := left + 1; right < n && h.less(right, left) {
			smallest = right
		}
		if !h.less(smallest, parent) {
			break
		}
		h.items[smallest], h.items[parent] = h.items[parent], h.items[smallest]
		parent = smallest
	}
	return top
}
