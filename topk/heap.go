package topk

import "container/heap"

// itemHeap is a heap.Interface whose root is the worst retained item,
// so eviction and replacement at the boundary are O(log K). The pos
// map tracks each ID's slice index for O(log K) overwrite updates.
type itemHeap struct {
	items []Item
	dir   Direction
	pos   map[string]int
}

func newItemHeap(dir Direction) *itemHeap {
	return &itemHeap{dir: dir, pos: make(map[string]int)}
}

func (h *itemHeap) Len() int { return len(h.items) }

// Less orders by "worse first": the worst item bubbles to the root.
func (h *itemHeap) Less(i, j int) bool {
	return better(h.items[j], h.items[i], h.dir)
}

func (h *itemHeap) Swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
	h.pos[h.items[i].ID] = i
	h.pos[h.items[j].ID] = j
}

func (h *itemHeap) Push(x any) {
	it := x.(Item)
	h.pos[it.ID] = len(h.items)
	h.items = append(h.items, it)
}

func (h *itemHeap) Pop() any {
	old := h.items
	n := len(old)
	it := old[n-1]
	h.items = old[:n-1]
	delete(h.pos, it.ID)
	return it
}

// add inserts a new item; caller must ensure the ID is absent.
func (h *itemHeap) add(it Item) {
	heap.Push(h, it)
}

// update repositions the item at index i after its score changed.
func (h *itemHeap) update(i int, it Item) {
	h.items[i] = it
	heap.Fix(h, i)
}

// evictRoot removes the worst item and returns it.
func (h *itemHeap) evictRoot() Item {
	return heap.Pop(h).(Item)
}

// root returns the worst retained item.
func (h *itemHeap) root() Item { return h.items[0] }
