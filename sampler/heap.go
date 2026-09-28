package sampler

// entry 是样本槽中的一条记录。
type entry struct {
	id     string
	weight float64
	key    float64
	order  int // 到达序号，从 0 开始
}

// minHeap 维护“当前最低”样本位于堆顶。
// 排序规则：键值小者更弱；键值相等时后到达（order 大）者更弱，
// 即并列情况下先到达者不会被后来者顶替。
type minHeap []*entry

func (h minHeap) Len() int { return len(h) }

func (h minHeap) lessAt(i, j int) bool {
	if h[i].key != h[j].key {
		return h[i].key < h[j].key
	}
	return h[i].order > h[j].order
}

func (h minHeap) swapAt(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *minHeap) push(e *entry) {
	*h = append(*h, e)
	h.up(len(*h) - 1)
}

func (h *minHeap) pop() *entry {
	old := *h
	n := len(old)
	top := old[0]
	last := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	if n > 1 {
		(*h)[0] = last
		h.down(0)
	}
	return top
}

func (h *minHeap) replaceTop(e *entry) {
	(*h)[0] = e
	h.down(0)
}

func (h minHeap) up(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if !h.lessAt(i, parent) {
			return
		}
		h.swapAt(i, parent)
		i = parent
	}
}

func (h minHeap) down(i int) {
	n := len(h)
	for {
		left := 2*i + 1
		if left >= n {
			return
		}
		smallest := left
		if right := left + 1; right < n && h.lessAt(right, left) {
			smallest = right
		}
		if !h.lessAt(smallest, i) {
			return
		}
		h.swapAt(i, smallest)
		i = smallest
	}
}
