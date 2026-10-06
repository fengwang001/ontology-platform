package inventory

// resHeap 是按到期时刻组织预留记录的最小堆（container/heap 接口）。
// 堆顶永远是当前最早到期的预留，因此清理过期预留时只需从堆顶弹出，
// 每条记录在其生命周期内至多被考察一次，考察次数不随历史订单总数增长。
type resHeap []*Reservation

func (h resHeap) Len() int { return len(h) }

func (h resHeap) Less(i, j int) bool {
	if h[i].Expiry != h[j].Expiry {
		return h[i].Expiry < h[j].Expiry
	}
	return h[i].OrderID < h[j].OrderID
}

func (h resHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *resHeap) Push(x any) { *h = append(*h, x.(*Reservation)) }

func (h *resHeap) Pop() any {
	old := *h
	n := len(old)
	r := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return r
}
