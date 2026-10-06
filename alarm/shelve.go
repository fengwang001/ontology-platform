package alarm

import "container/heap"

// expireEntry 是屏蔽到期堆中的一项。
// at 编码：at = int64(until)<<32 | int64(pointID)，
// 使堆按 (到期时刻, 点编号) 升序弹出。
type expireEntry struct {
	at int64
}

type expireHeap []expireEntry

func (h expireHeap) Len() int           { return len(h) }
func (h expireHeap) Less(i, j int) bool { return h[i].at < h[j].at }
func (h expireHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *expireHeap) Push(x any)        { *h = append(*h, x.(expireEntry)) }
func (h *expireHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

func encodeExpire(until, id int) int64 {
	return int64(until)<<32 | int64(uint32(id))
}

func decodeExpire(at int64) (until, id int) {
	return int(at >> 32), int(int32(at & 0xffffffff))
}

// pushExpire 登记一次屏蔽的到期时刻。
func (s *Service) pushExpire(p *activePoint) {
	heap.Push(&s.expireHeap, expireEntry{at: encodeExpire(p.shelveUntil, p.id)})
}
