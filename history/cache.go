package history

import "container/heap"

// docHeap 是按 (enteredAt, order) 排序的最小堆，
// 使"选出进入缓存最早者"的淘汰开销为 O(log n)，与缓存文档数无关。
type docHeap []*document

func (h docHeap) Len() int { return len(h) }

func (h docHeap) Less(i, j int) bool {
	if h[i].enteredAt != h[j].enteredAt {
		return h[i].enteredAt < h[j].enteredAt
	}
	return h[i].order < h[j].order
}

func (h docHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIndex = i
	h[j].heapIndex = j
}

func (h *docHeap) Push(x any) {
	d := x.(*document)
	d.heapIndex = len(*h)
	*h = append(*h, d)
}

func (h *docHeap) Pop() any {
	old := *h
	n := len(old)
	d := old[n-1]
	old[n-1] = nil
	d.heapIndex = -1
	*h = old[:n-1]
	return d
}

// docCache 是前进后退缓存的容量与存活期管理器。
// 同一文档标识在缓存中至多一份（文档离开当前位置时才会入缓存，天然唯一）。
type docCache struct {
	h docHeap
}

func (c *docCache) len() int { return len(c.h) }

func (c *docCache) insert(d *document) { heap.Push(&c.h, d) }

func (c *docCache) remove(d *document) { heap.Remove(&c.h, d.heapIndex) }

// earliest 返回进入缓存最早的文档，O(1)。
func (c *docCache) earliest() *document {
	if len(c.h) == 0 {
		return nil
	}
	return c.h[0]
}

// evictEarliest 弹出进入缓存最早的文档并将其标记为卸载，O(log n)。
func (c *docCache) evictEarliest() *document {
	if len(c.h) == 0 {
		return nil
	}
	d := heap.Pop(&c.h).(*document)
	d.status = statusUnloaded
	return d
}

// evictExpired 驱逐所有存活时长已达到 ttl 的文档；now-enteredAt == ttl 即视为过期。
func (c *docCache) evictExpired(now, ttl int64, onEvict func(*document)) {
	if ttl <= 0 {
		return
	}
	for {
		d := c.earliest()
		if d == nil || now-d.enteredAt < ttl {
			return
		}
		c.evictEarliest()
		if onEvict != nil {
			onEvict(d)
		}
	}
}
