// Package deadline 用最小堆维护各申请当前审批人的到期时刻。
package deadline

// Item 是堆中的一项：申请 req 的当前审批人在 due 到期。
type Item struct {
	Due int64
	Req string
	idx int // 堆内下标，由 Heap 维护
}

// Heap 是按 Due 排序的最小堆，可并发访问由调用方加锁保护。
type Heap struct {
	items []*Item
	index map[string]*Item
	// examined 为最近一次 Drain 考察（比较堆顶）的堆项数。
	examined int
}

// NewHeap 创建空堆。
func NewHeap() *Heap {
	return &Heap{index: make(map[string]*Item)}
}

// Len 返回堆中项数。
func (h *Heap) Len() int { return len(h.items) }

// Keys 返回堆中所有申请键（测试辅助）。
func (h *Heap) Keys() []string {
	out := make([]string, 0, len(h.items))
	for _, it := range h.items {
		out = append(out, it.Req)
	}
	return out
}

// Push 插入或更新 req 的到期项。
func (h *Heap) Push(req string, due int64) {
	if it, ok := h.index[req]; ok {
		it.Due = due
		h.siftUp(it.idx)
		h.siftDown(it.idx)
		return
	}
	it := &Item{Due: due, Req: req}
	it.idx = len(h.items)
	h.items = append(h.items, it)
	h.index[req] = it
	h.siftUp(it.idx)
}

// Peek 返回堆顶项（不弹出）；堆空返回 nil。
func (h *Heap) Peek() *Item {
	if len(h.items) == 0 {
		return nil
	}
	return h.items[0]
}

// Pop 弹出并返回堆顶；堆空返回 nil。
func (h *Heap) Pop() *Item {
	if len(h.items) == 0 {
		return nil
	}
	it := h.items[0]
	n := len(h.items) - 1
	h.swap(0, n)
	h.items[n] = nil
	h.items = h.items[:n]
	if n > 0 {
		h.siftDown(0)
	}
	delete(h.index, it.Req)
	it.idx = -1
	return it
}

// Remove 删除 req 的到期项（终局时调用）。
func (h *Heap) Remove(req string) {
	if it, ok := h.index[req]; ok {
		n := len(h.items) - 1
		i := it.idx
		if i != n {
			h.swap(i, n)
		}
		h.items[n] = nil
		h.items = h.items[:n]
		if i < n {
			h.siftUp(i)
			h.siftDown(i)
		}
		delete(h.index, req)
		it.idx = -1
	}
}

// Restore 撤销 Drain：同一 req 可能经历 pop->push(升级)->pop 多次，
// 其操作前的堆项就是弹出序列中该 req 的第一个项。oldDue 给出每个
// req 操作前的到期时刻；每个 req 仅放回最早弹出项并恢复旧 Due。
func (h *Heap) Restore(popped []*Item, oldDue map[string]int64) {
	restored := make(map[string]bool)
	for _, it := range popped {
		if restored[it.Req] {
			continue
		}
		restored[it.Req] = true
		h.Remove(it.Req)
		if d, ok := oldDue[it.Req]; ok {
			it.Due = d
		}
		it.idx = len(h.items)
		h.items = append(h.items, it)
		h.index[it.Req] = it
		h.siftUp(it.idx)
	}
}

// Examined 返回最近一次到期处理考察的堆项数。
func (h *Heap) Examined() int { return h.examined }

// BeginDrain 开始一次到期处理并清零考察计数。
func (h *Heap) BeginDrain() { h.examined = 0 }

// Examine 报告又考察了一次堆顶（含因未到期而停下的那次）。
func (h *Heap) Examine() { h.examined++ }

func (h *Heap) swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
	h.items[i].idx = i
	h.items[j].idx = j
}

func (h *Heap) less(i, j int) bool { return h.items[i].Due < h.items[j].Due }

func (h *Heap) siftUp(i int) {
	for i > 0 {
		p := (i - 1) / 2
		if !h.less(i, p) {
			return
		}
		h.swap(i, p)
		i = p
	}
}

func (h *Heap) siftDown(i int) {
	n := len(h.items)
	for {
		l := 2*i + 1
		if l >= n {
			return
		}
		j := l
		if r := l + 1; r < n && h.less(r, l) {
			j = r
		}
		if !h.less(j, i) {
			return
		}
		h.swap(i, j)
		i = j
	}
}
