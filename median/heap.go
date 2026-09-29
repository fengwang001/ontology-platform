package median

func newHalf(less func(a, b int) bool) half {
	return half{pending: make(map[int]int), less: less}
}

// half 表示一侧的堆及其待删（懒删除）账本。
type half struct {
	data    []int       // 堆数组（可能含等待清理的作废副本）
	pending map[int]int // 堆中等待清理的作废副本计数
	valid   int         // 本侧有效元素个数
	less    func(a, b int) bool
}

type maxHeap struct{ half }
type minHeap struct{ half }

func newMaxHeap() *maxHeap { return &maxHeap{newHalf(func(a, b int) bool { return a > b })} }
func newMinHeap() *minHeap { return &minHeap{newHalf(func(a, b int) bool { return a < b })} }

// push 压入一个有效元素。
func (h *half) push(v int) {
	h.data = append(h.data, v)
	h.valid++
	h.siftUp(len(h.data) - 1)
}

// top 返回堆顶原始值（调用方需保证已 prune，堆顶有效）。
func (h *half) top() int { return h.data[0] }

// popTop 弹出并返回堆顶（调用方需保证堆顶有效）。
func (h *half) popTop() int {
	v := h.data[0]
	last := len(h.data) - 1
	h.data[0] = h.data[last]
	h.data = h.data[:last]
	h.valid--
	if last > 0 {
		h.siftDown(0)
	}
	return v
}

// markStale 把一个有效值登记为作废副本：账本 +1、有效计数 -1。
func (h *half) markStale(v int) {
	h.pending[v]++
	h.valid--
}

// prune 不断弹出堆顶的作废副本，直到堆顶有效或堆为空。
func (h *half) prune() {
	for len(h.data) > 0 {
		top := h.data[0]
		if h.pending[top] == 0 {
			return
		}
		h.pending[top]--
		if h.pending[top] == 0 {
			delete(h.pending, top)
		}
		last := len(h.data) - 1
		h.data[0] = h.data[last]
		h.data = h.data[:last]
		if last > 0 {
			h.siftDown(0)
		}
	}
}

// moveTopTo 把本侧有效堆顶搬到 dst（保持有效计数正确）。
func (h *half) moveTopTo(dst *half) {
	v := h.popTop()
	dst.push(v)
}

func (h *half) siftUp(i int) {
	v := h.data[i]
	for i > 0 {
		parent := (i - 1) / 2
		if !h.less(v, h.data[parent]) {
			break
		}
		h.data[i] = h.data[parent]
		i = parent
	}
	h.data[i] = v
}

func (h *half) siftDown(i int) {
	n := len(h.data)
	v := h.data[i]
	for {
		left := 2*i + 1
		if left >= n {
			break
		}
		child := left
		if right := left + 1; right < n && h.less(h.data[right], h.data[left]) {
			child = right
		}
		if !h.less(h.data[child], v) {
			break
		}
		h.data[i] = h.data[child]
		i = child
	}
	h.data[i] = v
}

// cloneForCheck 复制堆数据与账本，供只读自检排空使用，不触碰原状态。
func (h *half) cloneForCheck() half {
	cp := half{
		data:    append([]int(nil), h.data...),
		pending: make(map[int]int, len(h.pending)),
		valid:   h.valid,
		less:    h.less,
	}
	for k, v := range h.pending {
		cp.pending[k] = v
	}
	return cp
}
