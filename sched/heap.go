package sched

import "ontology/tenant"

// vtHeap 是按 (vt, id) 排序的最小堆。lessCmp/swapCmp 仅用于测试度量，
// 由 Scheduler 在每次"选择"前后清零与读取。
type vtHeap struct {
	items   []*tenant.Tenant
	lessCmp int
	swapCmp int
}

func (h *vtHeap) Len() int { return len(h.items) }

// less 是唯一的排序比较点：所有堆父子比较都经过这里。
func (h *vtHeap) less(i, j int) bool {
	h.lessCmp++
	a, b := h.items[i], h.items[j]
	if a.VT() != b.VT() {
		return a.VT() < b.VT()
	}
	return a.ID() < b.ID()
}

func (h *vtHeap) swap(i, j int) {
	h.swapCmp++
	h.items[i], h.items[j] = h.items[j], h.items[i]
}

func (h *vtHeap) Push(x any) {
	h.items = append(h.items, x.(*tenant.Tenant))
}

func (h *vtHeap) Pop() any {
	n := len(h.items)
	x := h.items[n-1]
	h.items = h.items[:n-1]
	return x
}

// beginCmp / endCmp 界定"单次选择"的比较统计区间。
func (h *vtHeap) beginCmp() { h.lessCmp, h.swapCmp = 0, 0 }
func (h *vtHeap) endCmp() int {
	return h.lessCmp + h.swapCmp
}

// 以下方法满足 container/heap.Interface（Less/Swap 委托到计数版本）。
func (h *vtHeap) Less(i, j int) bool { return h.less(i, j) }
func (h *vtHeap) Swap(i, j int)      { h.swap(i, j) }

// TopVT 返回堆顶虚拟时间（堆空时为 0）。
func (h *vtHeap) TopVT() float64 {
	if len(h.items) == 0 {
		return 0
	}
	return h.items[0].VT()
}
