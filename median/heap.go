package median

// heapSide 是一个带懒删除的二叉堆。
// 被撤回的副本不会立即从切片中移除，而是登记到 pending：
// 只有当作废副本浮到堆顶时才在 prune 中被物理丢弃。
type heapSide struct {
	items   []int
	pending map[int]int // 仍物理存留在本堆中的作废副本计数
	valid   int         // 本堆中有效（未撤回）元素个数
	less    func(a, b int) bool
}

func newHeapSide(less func(a, b int) bool) *heapSide {
	return &heapSide{pending: make(map[int]int), less: less}
}

func (h *heapSide) rawLen() int { return len(h.items) }

// push 加入一个有效元素。
func (h *heapSide) push(v int) {
	h.items = append(h.items, v)
	h.siftUp(len(h.items) - 1)
	h.valid++
}

// popValid 弹出堆顶元素。调用方必须保证堆顶有效（已先 prune）。
func (h *heapSide) popValid() int {
	top := h.items[0]
	last := len(h.items) - 1
	h.items[0] = h.items[last]
	h.items = h.items[:last]
	if last > 0 {
		h.siftDown(0)
	}
	h.valid--
	return top
}

// top 返回堆顶元素（有效）。仅在 valid > 0 且 prune 之后调用。
func (h *heapSide) top() int { return h.items[0] }

// markDeleted 登记一个有效副本为作废：有效计数减一，待删计数加一。
func (h *heapSide) markDeleted(v int) {
	h.pending[v]++
	h.valid--
}

// prune 丢弃位于堆顶的作废副本，直到堆顶有效或堆为空。
func (h *heapSide) prune() {
	for len(h.items) > 0 {
		v := h.items[0]
		if h.pending[v] == 0 {
			return
		}
		h.pending[v]--
		if h.pending[v] == 0 {
			delete(h.pending, v)
		}
		last := len(h.items) - 1
		h.items[0] = h.items[last]
		h.items = h.items[:last]
		if last > 0 {
			h.siftDown(0)
		}
	}
}

// heapOrdered 校验整个物理切片满足堆序（包含尚未清理的作废副本）。
func (h *heapSide) heapOrdered() bool {
	for i := len(h.items) - 1; i > 0; i-- {
		if h.less(h.items[i], h.items[(i-1)/2]) {
			return false
		}
	}
	return true
}

func (h *heapSide) siftUp(i int) {
	v := h.items[i]
	for i > 0 {
		parent := (i - 1) / 2
		if !h.less(v, h.items[parent]) {
			break
		}
		h.items[i] = h.items[parent]
		i = parent
	}
	h.items[i] = v
}

func (h *heapSide) siftDown(i int) {
	n := len(h.items)
	v := h.items[i]
	for {
		left := 2*i + 1
		if left >= n {
			break
		}
		child := left
		if right := left + 1; right < n && h.less(h.items[right], h.items[left]) {
			child = right
		}
		if !h.less(h.items[child], v) {
			break
		}
		h.items[i] = h.items[child]
		i = child
	}
	h.items[i] = v
}
