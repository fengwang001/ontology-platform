package gdsfcache

import "math/big"

// entry is a single cached artifact.
type entry struct {
	key   string
	size  int64
	cost  int64
	freq  int64
	h     *big.Rat
	last  int64
	index int // position within the priority queue
}

// entryHeap is a min-heap ordered by (H, last): smallest H first, ties
// broken by the smaller logical tick. It implements heap.Interface.
type entryHeap struct {
	entries []*entry
	cmps    *int64 // counts H comparisons for complexity assertions
}

func (pq *entryHeap) Len() int { return len(pq.entries) }

func (pq *entryHeap) Less(i, j int) bool {
	*pq.cmps++
	if c := pq.entries[i].h.Cmp(pq.entries[j].h); c != 0 {
		return c < 0
	}
	return pq.entries[i].last < pq.entries[j].last
}

func (pq *entryHeap) Swap(i, j int) {
	pq.entries[i], pq.entries[j] = pq.entries[j], pq.entries[i]
	pq.entries[i].index = i
	pq.entries[j].index = j
}

func (pq *entryHeap) Push(x any) {
	e := x.(*entry)
	e.index = len(pq.entries)
	pq.entries = append(pq.entries, e)
}

func (pq *entryHeap) Pop() any {
	old := pq.entries
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	e.index = -1
	pq.entries = old[:n-1]
	return e
}
