package sampler

import "container/heap"

// decEntry is one cached trace decision.
type decEntry struct {
	traceID   string
	keep      bool
	reason    string
	decidedAt int64
}

// decRec pairs a cache record with its heap index.
type decRec struct {
	decEntry
	idx int
}

// decHeap orders records by (decidedAt, traceID) ascending.
type decHeap []*decRec

func (h decHeap) Len() int { return len(h) }
func (h decHeap) Less(i, j int) bool {
	if h[i].decidedAt != h[j].decidedAt {
		return h[i].decidedAt < h[j].decidedAt
	}
	return h[i].traceID < h[j].traceID
}
func (h decHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].idx = i
	h[j].idx = j
}
func (h *decHeap) Push(x any) {
	r := x.(*decRec)
	r.idx = len(*h)
	*h = append(*h, r)
}
func (h *decHeap) Pop() any {
	old := *h
	r := old[len(old)-1]
	*h = old[:len(old)-1]
	return r
}

// decisionCache is a bounded set of decisions with per-entry TTL. A zero
// ttl or cap disables caching: every lookup misses.
type decisionCache struct {
	ttl  int64
	cap  int64
	live map[string]*decRec
	old  decHeap
}

func newDecisionCache(ttl, cap int64) *decisionCache {
	if ttl <= 0 || cap <= 0 {
		return &decisionCache{}
	}
	return &decisionCache{ttl: ttl, cap: cap, live: make(map[string]*decRec)}
}

// get reports whether traceID has a live decision at now. Expired records
// behave as if absent (and are removed eagerly).
func (c *decisionCache) get(traceID string, now int64) (decEntry, bool) {
	if c.live == nil {
		return decEntry{}, false
	}
	r, ok := c.live[traceID]
	if !ok {
		return decEntry{}, false
	}
	if r.decidedAt+c.ttl <= now {
		heap.Remove(&c.old, r.idx)
		delete(c.live, traceID)
		return decEntry{}, false
	}
	return r.decEntry, true
}

// put stores a decision: first all expired entries are reaped, then the
// smallest (decidedAt, traceID) entries are evicted while over capacity.
func (c *decisionCache) put(e decEntry, now int64) {
	if c.live == nil {
		return
	}
	for c.old.Len() > 0 {
		v := c.old[0]
		if v.decidedAt+c.ttl > now {
			break
		}
		heap.Pop(&c.old)
		delete(c.live, v.traceID)
	}
	if r, ok := c.live[e.traceID]; ok {
		r.decEntry = e
		heap.Fix(&c.old, r.idx)
	} else {
		r := &decRec{decEntry: e}
		c.live[e.traceID] = r
		heap.Push(&c.old, r)
	}
	for int64(len(c.live)) > c.cap {
		v := heap.Pop(&c.old).(*decRec)
		delete(c.live, v.traceID)
	}
}
