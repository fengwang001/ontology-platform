package admission

import "sort"

// expiryHeap 的不变量：
//   - entries 是按 (deadline, seq) 排序的最小堆；
//   - index 记录每个等待者在堆中的位置，删除任意条目 O(log n)；
//   - 入队与超时弹出的复杂度只与实际移动的条目有关，与队列总长无关。
type expiryHeap struct {
	entries []*waiter
	index   map[*waiter]int
}

func newExpiryHeap() *expiryHeap { return &expiryHeap{index: map[*waiter]int{}} }

func (h *expiryHeap) len() int { return len(h.entries) }

func (h *expiryHeap) push(w *waiter) {
	h.index[w] = len(h.entries)
	h.entries = append(h.entries, w)
	h.siftUp(len(h.entries) - 1)
}

func (h *expiryHeap) remove(w *waiter) {
	idx, ok := h.index[w]
	if !ok {
		return
	}
	last := len(h.entries) - 1
	if idx != last {
		h.swap(idx, last)
	}
	delete(h.index, w)
	h.entries = h.entries[:last]
	if idx < last {
		h.siftUp(idx)
		h.siftDown(idx)
	}
}

// peekDeadline 返回最早超时时刻；空堆返回 false。
func (h *expiryHeap) peekDeadline() (Time, bool) {
	if len(h.entries) == 0 {
		return 0, false
	}
	return h.entries[0].deadline, true
}

// popExpired 弹出并返回所有 deadline <= now 的等待者（deadline 升序）。
func (h *expiryHeap) popExpired(now Time) []*waiter {
	var out []*waiter
	for len(h.entries) > 0 && h.entries[0].deadline <= now {
		w := h.entries[0]
		last := len(h.entries) - 1
		if last != 0 {
			h.swap(0, last)
		}
		delete(h.index, w)
		h.entries = h.entries[:last]
		if last > 0 {
			h.siftDown(0)
		}
		out = append(out, w)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].seq < out[j].seq })
	return out
}

func (h *expiryHeap) less(i, j int) bool {
	a, b := h.entries[i], h.entries[j]
	if a.deadline != b.deadline {
		return a.deadline < b.deadline
	}
	return a.seq < b.seq
}

func (h *expiryHeap) swap(i, j int) {
	h.entries[i], h.entries[j] = h.entries[j], h.entries[i]
	h.index[h.entries[i]] = i
	h.index[h.entries[j]] = j
}

func (h *expiryHeap) siftUp(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if !h.less(i, parent) {
			return
		}
		h.swap(i, parent)
		i = parent
	}
}

func (h *expiryHeap) siftDown(i int) {
	n := len(h.entries)
	for {
		left := 2*i + 1
		right := left + 1
		smallest := i
		if left < n && h.less(left, smallest) {
			smallest = left
		}
		if right < n && h.less(right, smallest) {
			smallest = right
		}
		if smallest == i {
			return
		}
		h.swap(i, smallest)
		i = smallest
	}
}
