package matcher

import "container/heap"

// priceHeap is a min-heap of int64 prices. Buy-side "best price" (maximum)
// is obtained by pushing the negated price into the same structure.
type priceHeap []int64

func (h priceHeap) Len() int           { return len(h) }
func (h priceHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h priceHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *priceHeap) Push(x any)        { *h = append(*h, x.(int64)) }
func (h *priceHeap) Pop() any {
	old := *h
	n := len(old)
	v := old[n-1]
	*h = old[:n-1]
	return v
}

// levelIndex tracks the non-empty price levels on one side plus a heap that
// yields the best one. Entries are deleted lazily: a price stays in the heap
// until it reaches the top and is no longer present in the active set. Each
// price is pushed at most once per non-empty episode, so the amortized cost
// of lazy pops over the engine lifetime is O(total pushes log N).
type levelIndex struct {
	heap      priceHeap
	active    map[int64]*priceLevel
	negate    bool
	stalePops *int64
}

func newLevelIndex(negate bool, stalePops *int64) *levelIndex {
	return &levelIndex{
		active:    make(map[int64]*priceLevel),
		negate:    negate,
		stalePops: stalePops,
	}
}

func (idx *levelIndex) key(price int64) int64 {
	if idx.negate {
		return -price
	}
	return price
}

// add inserts a newly non-empty level. It must not already be active.
func (idx *levelIndex) add(l *priceLevel) {
	idx.active[l.price] = l
	heap.Push(&idx.heap, idx.key(l.price))
}

func (idx *levelIndex) remove(price int64) {
	delete(idx.active, price)
}

func (idx *levelIndex) get(price int64) *priceLevel {
	return idx.active[price]
}

// cleanup drops stale prices from the heap top.
func (idx *levelIndex) cleanup() {
	for len(idx.heap) > 0 {
		top := idx.heap[0]
		price := top
		if idx.negate {
			price = -top
		}
		if _, ok := idx.active[price]; ok {
			return
		}
		heap.Pop(&idx.heap)
		if idx.stalePops != nil {
			*idx.stalePops++
		}
	}
}

// best returns the best non-empty level or nil.
func (idx *levelIndex) best() *priceLevel {
	idx.cleanup()
	if len(idx.heap) == 0 {
		return nil
	}
	top := idx.heap[0]
	if idx.negate {
		top = -top
	}
	return idx.active[top]
}
