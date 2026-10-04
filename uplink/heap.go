package uplink

import "container/heap"

const (
	kindPending = iota
	kindHeartbeat
)

// entry 是事件堆中的一条条目；每个测点同时至多存在待报、心跳两条。
// 状态变化时由 refresh 同步重算并 Fix/Remove，绝不留惰性废条目。
type entry struct {
	point string
	ek    int
	at    int64
	index int
}

type eventHeap []*entry

func (h eventHeap) Len() int { return len(h) }

func (h eventHeap) Less(i, j int) bool {
	if h[i].at != h[j].at {
		return h[i].at < h[j].at
	}
	if h[i].point != h[j].point {
		return h[i].point < h[j].point
	}
	// 同测点同刻：待报先于心跳。
	return h[i].ek < h[j].ek
}

func (h eventHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *eventHeap) Push(x any) {
	e := x.(*entry)
	e.index = len(*h)
	*h = append(*h, e)
}

func (h *eventHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	e.index = -1
	return e
}

func (h *eventHeap) remove(e *entry) {
	if e.index >= 0 {
		heap.Remove(h, e.index)
	}
}

func (h *eventHeap) fix(e *entry)  { heap.Fix(h, e.index) }
func (h *eventHeap) push(e *entry) { heap.Push(h, e) }
func (h *eventHeap) pop() *entry   { return heap.Pop(h).(*entry) }
