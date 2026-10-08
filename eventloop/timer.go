package eventloop

// timer 是一个定时器。到期时刻相同的定时器按注册序（seq）入队。
type timer struct {
	handle    Handle
	due       int64
	seq       uint64
	depth     int
	cb        func()
	cancelled bool
	task      *task // 到期后生成的定时器任务（入队时刻取到期时刻）
}

// timerHeap 是按 (到期时刻, 注册序) 排序的最小堆。
type timerHeap []*timer

func (h timerHeap) Len() int { return len(h) }
func (h timerHeap) Less(i, j int) bool {
	if h[i].due != h[j].due {
		return h[i].due < h[j].due
	}
	return h[i].seq < h[j].seq
}
func (h timerHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *timerHeap) Push(x any)   { *h = append(*h, x.(*timer)) }
func (h *timerHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return it
}

// idleTimeout 是空闲回调的超时条目，到期后作为内部源任务入队。
type idleTimeout struct {
	deadline int64
	seq      uint64
	cb       *idleCallback
}

// idleTimeoutHeap 是按 (超时时刻, 注册序) 排序的最小堆。
type idleTimeoutHeap []idleTimeout

func (h idleTimeoutHeap) Len() int { return len(h) }
func (h idleTimeoutHeap) Less(i, j int) bool {
	if h[i].deadline != h[j].deadline {
		return h[i].deadline < h[j].deadline
	}
	return h[i].seq < h[j].seq
}
func (h idleTimeoutHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *idleTimeoutHeap) Push(x any)   { *h = append(*h, x.(idleTimeout)) }
func (h *idleTimeoutHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}
