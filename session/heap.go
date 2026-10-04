package session

// sessionHeap 是按到期时刻排序的最小堆（container/heap），
// 支持 Fix 原位更新与 Remove 删除，不使用惰性删除。
// 到期时刻相同按会话号排序，保证确定性。
type sessionHeap []*session

func (h sessionHeap) Len() int { return len(h) }

func (h sessionHeap) Less(i, j int) bool {
	if h[i].expiry != h[j].expiry {
		return h[i].expiry < h[j].expiry
	}
	return h[i].sid < h[j].sid
}

func (h sessionHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIndex = i
	h[j].heapIndex = j
}

func (h *sessionHeap) Push(x any) {
	sess := x.(*session)
	sess.heapIndex = len(*h)
	*h = append(*h, sess)
}

func (h *sessionHeap) Pop() any {
	old := *h
	n := len(old)
	sess := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return sess
}
