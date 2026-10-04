package lessor

import "container/heap"

// leaseHeap 是按 (x, id) 升序的到期最小堆。
// 堆内元素与 Lessor.leases 始终保持一致：任何改变到期时刻或删除租约的
// 操作都会立即 Fix/Remove/Push 或整体重建，不留惰性跳过的过期堆项。
type leaseHeap struct {
	items []*lease
	pos   map[int64]int // lease id -> items 下标
}

func (h *leaseHeap) Len() int { return len(h.items) }

func (h *leaseHeap) Less(i, j int) bool {
	a, b := h.items[i], h.items[j]
	if a.x != b.x {
		return a.x < b.x
	}
	return a.id < b.id
}

func (h *leaseHeap) Swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
	h.pos[h.items[i].id] = i
	h.pos[h.items[j].id] = j
}

func (h *leaseHeap) Push(v any) {
	l := v.(*lease)
	h.pos[l.id] = len(h.items)
	h.items = append(h.items, l)
}

func (h *leaseHeap) Pop() any {
	old := h.items
	n := len(old)
	l := old[n-1]
	old[n-1] = nil
	h.items = old[:n-1]
	delete(h.pos, l.id)
	return l
}

// top 返回堆顶租约；堆空时返回 nil。
func (h *leaseHeap) top() *lease {
	if len(h.items) == 0 {
		return nil
	}
	return h.items[0]
}

// push 将租约插入堆。
func (h *leaseHeap) push(l *lease) {
	heap.Push(h, l)
}

// fix 在租约到期时刻变化后恢复堆序。
func (h *leaseHeap) fix(l *lease) {
	heap.Fix(h, h.pos[l.id])
}

// remove 将租约从堆中删除。
func (h *leaseHeap) remove(l *lease) {
	heap.Remove(h, h.pos[l.id])
}

// rebuild 在所有租约到期时刻被批量改写后（Promote）整体重建堆。
func (h *leaseHeap) rebuild() {
	heap.Init(h)
}
