package kerberos

import "container/heap"

// replayKey identifies a replay cache entry.
type replayKey struct {
	id   int64
	time int64
}

type replayEntry struct {
	key    replayKey
	expire int64
}

type replayHeap []replayEntry

func (h replayHeap) Len() int           { return len(h) }
func (h replayHeap) Less(i, j int) bool { return h[i].expire < h[j].expire }
func (h replayHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *replayHeap) Push(x any)        { *h = append(*h, x.(replayEntry)) }
func (h *replayHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// replayCache keeps entries whose authTime is within [now-S, now+S]: an entry
// is treated as absent once now > authTime+S, i.e. expireAt > now.
type replayCache struct {
	h        replayHeap
	m        map[replayKey]struct{}
	skew     int64
	popCount int64
}

func newReplayCache(skew int64) replayCache {
	return replayCache{m: make(map[replayKey]struct{}), skew: skew}
}

// evict removes entries with now > authTime+S. The number of heap pops never
// exceeds the number of items becoming due at now plus one stop probe, so the
// cost is amortized O(1) per accepted operation.
func (c *replayCache) evict(now int64) {
	for len(c.h) > 0 {
		top := c.h[0]
		if top.expire >= now {
			// one stop probe per accepted operation
			c.popCount++
			return
		}
		c.popCount++
		heap.Pop(&c.h)
		delete(c.m, top.key)
	}
}

func (c *replayCache) contains(key replayKey) bool {
	_, ok := c.m[key]
	return ok
}

func (c *replayCache) add(key replayKey, now int64) {
	heap.Push(&c.h, replayEntry{key: key, expire: key.time + c.skew})
	_ = now
	c.m[key] = struct{}{}
}
