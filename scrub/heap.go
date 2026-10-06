package scrub

import "container/heap"

// dueHeap 是到期块索引：以 nextDue（下次到期时刻）为键的索引堆，
// 支撑 SelectDue 只随已到期块数与返回数量增长的开销。
// 从未巡检的块键为 math.MinInt64，因此总是最先到期。
//
// 每个块在堆中常驻恰好一个条目，通过 byBlock 与 index 实现
// O(log n) 的就地更新与删除，避免懒删除造成的陈旧条目堆积。
type dueHeap struct {
	entries []*dueEntry
	byBlock map[uint64]*dueEntry
}

type dueEntry struct {
	blockID uint64
	key     int64 // nextDue = lastScrub + minInterval；未巡检为 math.MinInt64
	index   int   // 在 entries 中的位置，供 O(log n) 更新与删除
}

func newDueHeap() *dueHeap {
	return &dueHeap{byBlock: make(map[uint64]*dueEntry)}
}

// upsert 插入或更新一个块的到期键。
func (h *dueHeap) upsert(blockID uint64, key int64) {
	if e, ok := h.byBlock[blockID]; ok {
		e.key = key
		heap.Fix(h, e.index)
		return
	}
	heap.Push(h, &dueEntry{blockID: blockID, key: key})
}

func (h *dueHeap) remove(blockID uint64) {
	if e, ok := h.byBlock[blockID]; ok {
		heap.Remove(h, e.index)
	}
}

// peek 返回堆顶（最早到期）条目，堆空时返回 nil。
func (h *dueHeap) peek() *dueEntry {
	if len(h.entries) == 0 {
		return nil
	}
	return h.entries[0]
}

// pop 弹出堆顶条目。
func (h *dueHeap) pop() *dueEntry {
	if len(h.entries) == 0 {
		return nil
	}
	return heap.Pop(h).(*dueEntry)
}

// pushBack 将弹出的条目重新放回（SelectDue 只读语义的实现手段）。
func (h *dueHeap) pushBack(e *dueEntry) {
	heap.Push(h, e)
}

// 以下为 container/heap.Interface 的实现。
// 堆序：key 小者优先，key 并列时块号小者优先。

func (h *dueHeap) Len() int { return len(h.entries) }

func (h *dueHeap) Less(i, j int) bool {
	a, b := h.entries[i], h.entries[j]
	if a.key != b.key {
		return a.key < b.key
	}
	return a.blockID < b.blockID
}

func (h *dueHeap) Swap(i, j int) {
	h.entries[i], h.entries[j] = h.entries[j], h.entries[i]
	h.entries[i].index = i
	h.entries[j].index = j
}

func (h *dueHeap) Push(x any) {
	e := x.(*dueEntry)
	e.index = len(h.entries)
	h.entries = append(h.entries, e)
	h.byBlock[e.blockID] = e
}

func (h *dueHeap) Pop() any {
	n := len(h.entries)
	e := h.entries[n-1]
	h.entries[n-1] = nil
	h.entries = h.entries[:n-1]
	delete(h.byBlock, e.blockID)
	e.index = -1
	return e
}
