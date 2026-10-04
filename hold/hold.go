// Package hold 维护一次启动内尚未输出（待定）的遥测记录。
//
// 记录按 (k, 到达序) 组成最小堆，保证 Sync 释放 k<=K 的记录时
// 被考察的记录数不超过释放数加 1。本包不做并发保护，调用方须持锁。
package hold

// Record 是一条待定记录的载荷与定位信息。
type Record struct {
	K       int64
	Arrival int64
	Payload any
}

// Heap 是待定记录的最小堆。
type Heap struct {
	items []Record
}

// New 创建空待定堆。
func New() *Heap { return &Heap{} }

// Len 返回待定记录数。
func (h *Heap) Len() int { return len(h.items) }

// Push 加入一条待定记录。
func (h *Heap) Push(r Record) {
	h.items = append(h.items, r)
	h.siftUp(len(h.items) - 1)
}

// DrainLE 依次取出并删除所有 K<=kmax 的记录，按 (K, Arrival) 升序返回。
// examined 为过程中考察（触及）的堆顶记录数，满足
// examined <= len(out)+1，与堆内记录总数无关。
func (h *Heap) DrainLE(kmax int64) (out []Record, examined int) {
	for len(h.items) > 0 {
		examined++
		top := h.items[0]
		if top.K > kmax {
			break
		}
		out = append(out, top)
		h.popRoot()
	}
	return out, examined
}

// DrainAll 依次取出全部记录，按 (K, Arrival) 升序返回。
func (h *Heap) DrainAll() []Record {
	out, _ := h.DrainLE(int64(^uint64(0) >> 1))
	return out
}

func (h *Heap) less(i, j int) bool {
	a, b := h.items[i], h.items[j]
	return a.K < b.K || (a.K == b.K && a.Arrival < b.Arrival)
}

func (h *Heap) siftUp(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if !h.less(i, parent) {
			return
		}
		h.items[i], h.items[parent] = h.items[parent], h.items[i]
		i = parent
	}
}

func (h *Heap) popRoot() {
	last := len(h.items) - 1
	h.items[0] = h.items[last]
	h.items = h.items[:last]
	i := 0
	for {
		left := 2*i + 1
		if left >= len(h.items) {
			return
		}
		smallest := left
		if right := left + 1; right < len(h.items) && h.less(right, left) {
			smallest = right
		}
		if !h.less(smallest, i) {
			return
		}
		h.items[i], h.items[smallest] = h.items[smallest], h.items[i]
		i = smallest
	}
}
