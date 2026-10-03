package sampler

import (
	"container/heap"

	"ontology/tailsampler/span"
)

// bufItem is one buffered trace inside the min-heap ordered by
// (LastSeen, traceID). The heap gives O(log n) eviction of the
// least-recently-seen trace and lets Tick pop only silent traces.
type bufItem struct {
	traceID string
	entry   *span.Entry
	idx     int // position inside bufHeap, maintained by Swap
}

type bufHeap []*bufItem

func (h bufHeap) Len() int { return len(h) }

func (h bufHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.entry.LastSeen != b.entry.LastSeen {
		return a.entry.LastSeen < b.entry.LastSeen
	}
	return a.traceID < b.traceID
}

func (h bufHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].idx = i
	h[j].idx = j
}

func (h *bufHeap) Push(x any) {
	it := x.(*bufItem)
	it.idx = len(*h)
	*h = append(*h, it)
}

func (h *bufHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	it.idx = -1
	*h = old[:n-1]
	return it
}

// cacheItem is one remembered decision inside the min-heap ordered by
// (decidedAt, traceID), used for expiry sweeps and capacity eviction.
type cacheItem struct {
	traceID   string
	decidedAt uint64
	keep      bool
	idx       int
}

type cacheHeap []*cacheItem

func (h cacheHeap) Len() int { return len(h) }

func (h cacheHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.decidedAt != b.decidedAt {
		return a.decidedAt < b.decidedAt
	}
	return a.traceID < b.traceID
}

func (h cacheHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].idx = i
	h[j].idx = j
}

func (h *cacheHeap) Push(x any) {
	it := x.(*cacheItem)
	it.idx = len(*h)
	*h = append(*h, it)
}

func (h *cacheHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	it.idx = -1
	*h = old[:n-1]
	return it
}

// decisionCache remembers the keep/drop outcome of recently decided traces
// so late spans can follow the existing decision instead of re-buffering.
// Entries expire lazily on read and are swept eagerly on every write.
type decisionCache struct {
	td   uint64 // TTL: entry is gone once decidedAt+td <= now
	cmax uint64 // capacity bound
	m    map[string]*cacheItem
	h    cacheHeap // min by (decidedAt, traceID)
}

func newDecisionCache(td, cmax uint64) *decisionCache {
	return &decisionCache{td: td, cmax: cmax, m: make(map[string]*cacheItem)}
}

func (c *decisionCache) expired(it *cacheItem, now uint64) bool {
	return it.decidedAt+c.td <= now
}

// lookup returns the remembered keep flag for traceID, or ok=false when the
// trace is unknown or its decision has expired. Expired entries are left in
// place; the next store sweeps them.
func (c *decisionCache) lookup(traceID string, now uint64) (keep, ok bool) {
	it, found := c.m[traceID]
	if !found || c.expired(it, now) {
		return false, false
	}
	return it.keep, true
}

// store records a fresh decision taken at now. It first sweeps every expired
// entry, then evicts the (decidedAt, traceID)-smallest entries until the
// cache fits within cmax.
func (c *decisionCache) store(traceID string, keep bool, now uint64) {
	for len(c.h) > 0 && c.expired(c.h[0], now) {
		victim := heap.Pop(&c.h).(*cacheItem)
		delete(c.m, victim.traceID)
	}
	if old, found := c.m[traceID]; found {
		// Unreachable in a correct run (a trace is decided at most once per
		// cache lifetime), but keeps the map and heap consistent defensively.
		heap.Remove(&c.h, old.idx)
		delete(c.m, traceID)
	}
	it := &cacheItem{traceID: traceID, decidedAt: now, keep: keep}
	heap.Push(&c.h, it)
	c.m[traceID] = it
	for uint64(len(c.m)) > c.cmax {
		victim := heap.Pop(&c.h).(*cacheItem)
		delete(c.m, victim.traceID)
	}
}
