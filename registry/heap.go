package registry

import "container/heap"

// heapEntry 是过期最小堆的一项；version 用于识别已被替换而作废的项。
type heapEntry struct {
	expireAt int64
	id       string
	version  uint64
	index    int
}

type expiryHeap []*heapEntry

func (h expiryHeap) Len() int { return len(h) }

func (h expiryHeap) Less(i, j int) bool {
	if h[i].expireAt != h[j].expireAt {
		return h[i].expireAt < h[j].expireAt
	}
	return h[i].id < h[j].id
}

func (h expiryHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *expiryHeap) Push(x any) {
	e := x.(*heapEntry)
	e.index = len(*h)
	*h = append(*h, e)
}

func (h *expiryHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	e.index = -1
	*h = old[:n-1]
	return e
}

// pushExpiry 登记一条结束记录的过期时刻 endAt+R。
func (g *Registry) pushExpiry(id string, version uint64, endAt int64) {
	heap.Push(&g.expiry, &heapEntry{expireAt: endAt + g.r, id: id, version: version})
}

// purgeExpired 惰性清除 now 时刻已到期的存活记录。
// 每轮只考察一次堆顶：一旦堆顶未到期（或堆空）立即停止。
// 故一次操作考察的堆项数不超过到期项数 + 1（含顺带弹出的作废项）。
func (g *Registry) purgeExpired(now int64) {
	for g.expiry.Len() > 0 {
		g.peep++
		top := g.expiry[0]
		if top.expireAt > now {
			return
		}
		heap.Pop(&g.expiry)
		if cur, ok := g.live[top.id]; ok && cur.version == top.version {
			delete(g.live, top.id)
		}
	}
}
