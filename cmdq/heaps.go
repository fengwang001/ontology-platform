package cmdq

import "sort"

// sortByFirst 按首次投递窗口起点排序，相等时按 seq（首次投递先后总序）。
func sortByFirst(cs []*cmd) {
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].first != cs[j].first {
			return cs[i].first < cs[j].first
		}
		return cs[i].seq < cs[j].seq
	})
}

// expHeap 按 expire 升序的最小堆。
type expHeap struct {
	q    *Queue
	data []*cmd
}

func (h *expHeap) Len() int   { return len(h.data) }
func (h *expHeap) peek() *cmd { return h.data[0] }
func (h *expHeap) Less(i, j int) bool {
	a, b := h.data[i], h.data[j]
	if a.expire != b.expire {
		return a.expire < b.expire
	}
	return a.seq < b.seq
}
func (h *expHeap) Swap(i, j int) {
	h.data[i], h.data[j] = h.data[j], h.data[i]
	h.data[i].expIdx = i
	h.data[j].expIdx = j
}
func (h *expHeap) Push(x any) {
	c := x.(*cmd)
	c.expIdx = len(h.data)
	h.data = append(h.data, c)
}
func (h *expHeap) Pop() any {
	n := len(h.data)
	c := h.data[n-1]
	c.expIdx = -1
	h.data = h.data[:n-1]
	return c
}

// seqHeap 是某一 prio 桶内按 seq 升序的堆。
type seqHeap struct {
	q    *Queue
	data []*cmd
}

func (h *seqHeap) Len() int           { return len(h.data) }
func (h *seqHeap) peek() *cmd         { return h.data[0] }
func (h *seqHeap) Less(i, j int) bool { return h.data[i].seq < h.data[j].seq }
func (h *seqHeap) Swap(i, j int) {
	h.data[i], h.data[j] = h.data[j], h.data[i]
	h.data[i].pendIdx = i
	h.data[j].pendIdx = j
}
func (h *seqHeap) Push(x any) {
	c := x.(*cmd)
	c.pendIdx = len(h.data)
	h.data = append(h.data, c)
}
func (h *seqHeap) Pop() any {
	n := len(h.data)
	c := h.data[n-1]
	c.pendIdx = -1
	h.data = h.data[:n-1]
	return c
}

// firstHeap 是可重投待确认指令按首次投递窗口起点升序的堆；起点相同
// 时按 seq（即首次投递先后的总序）。
type firstHeap struct {
	q    *Queue
	data []*cmd
}

func (h *firstHeap) Len() int   { return len(h.data) }
func (h *firstHeap) peek() *cmd { return h.data[0] }
func (h *firstHeap) Less(i, j int) bool {
	a, b := h.data[i], h.data[j]
	if a.first != b.first {
		return a.first < b.first
	}
	return a.seq < b.seq
}
func (h *firstHeap) Swap(i, j int) {
	h.data[i], h.data[j] = h.data[j], h.data[i]
	h.data[i].eligIdx = i
	h.data[j].eligIdx = j
}
func (h *firstHeap) Push(x any) {
	c := x.(*cmd)
	c.eligIdx = len(h.data)
	h.data = append(h.data, c)
}
func (h *firstHeap) Pop() any {
	n := len(h.data)
	c := h.data[n-1]
	c.eligIdx = -1
	h.data = h.data[:n-1]
	return c
}
