package history

import (
	"container/heap"
	"time"
)

// docHeap is a min-heap of cached documents ordered by (cachedAt, order),
// so picking the eviction victim costs O(log n), never a linear scan.
type docHeap []*Document

func (h docHeap) Len() int { return len(h) }

func (h docHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if !a.cachedAt.Equal(b.cachedAt) {
		return a.cachedAt.Before(b.cachedAt)
	}
	return a.order < b.order
}

func (h docHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heap = i
	h[j].heap = j
}

func (h *docHeap) Push(x any) {
	d := x.(*Document)
	d.heap = len(*h)
	*h = append(*h, d)
}

func (h *docHeap) Pop() any {
	old := *h
	n := len(old)
	d := old[n-1]
	old[n-1] = nil
	d.heap = -1
	*h = old[:n-1]
	return d
}

// bfCache is the back/forward cache: a bounded set of documents with
// capacity eviction (oldest entry first) and TTL expiration
// (age == ttl already counts as expired).
type bfCache struct {
	capacity int
	ttl      time.Duration
	h        docHeap
	byID     map[uint64]*Document
	orderSeq uint64
}

func newBFCache(capacity int, ttl time.Duration) *bfCache {
	return &bfCache{capacity: capacity, ttl: ttl, byID: make(map[uint64]*Document)}
}

// insert caches d and enforces TTL and capacity. Returns the evicted
// documents (possibly including d itself) split by reason.
func (c *bfCache) insert(d *Document, now time.Time) (expired, overflow []*Document) {
	d.status = DocCached
	d.cachedAt = now
	d.order = c.orderSeq
	c.orderSeq++
	heap.Push(&c.h, d)
	c.byID[d.ID] = d
	expired = c.evictExpired(now)
	for c.h.Len() > c.capacity {
		overflow = append(overflow, c.popOldest())
	}
	return expired, overflow
}

func (c *bfCache) popOldest() *Document {
	d := heap.Pop(&c.h).(*Document)
	delete(c.byID, d.ID)
	return d
}

// remove takes a document out of the cache (restore or truncation).
func (c *bfCache) remove(id uint64) *Document {
	d, ok := c.byID[id]
	if !ok {
		return nil
	}
	heap.Remove(&c.h, d.heap)
	delete(c.byID, id)
	return d
}

// evictExpired unloads every document whose age reached the TTL.
func (c *bfCache) evictExpired(now time.Time) (evicted []*Document) {
	for c.h.Len() > 0 && now.Sub(c.h[0].cachedAt) >= c.ttl {
		evicted = append(evicted, c.popOldest())
	}
	return evicted
}

func (c *bfCache) contains(id uint64) bool {
	_, ok := c.byID[id]
	return ok
}

func (c *bfCache) size() int { return c.h.Len() }
