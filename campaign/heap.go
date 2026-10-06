package campaign

// readyHeap holds exactly the Pending devices, ordered by (readyAt, id).
type readyHeap []*device

func (h readyHeap) Len() int { return len(h) }

func (h readyHeap) Less(i, j int) bool {
	if h[i].readyAt != h[j].readyAt {
		return h[i].readyAt < h[j].readyAt
	}
	return h[i].id < h[j].id
}

func (h readyHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].readyIdx = i
	h[j].readyIdx = j
}

func (h *readyHeap) Push(x any) {
	d := x.(*device)
	d.readyIdx = len(*h)
	*h = append(*h, d)
}

func (h *readyHeap) Pop() any {
	old := *h
	n := len(old)
	d := old[n-1]
	old[n-1] = nil
	d.readyIdx = -1
	*h = old[:n-1]
	return d
}

// inflightHeap holds exactly the InFlight devices, ordered by (dl, id).
type inflightHeap []*device

func (h inflightHeap) Len() int { return len(h) }

func (h inflightHeap) Less(i, j int) bool {
	if h[i].dl != h[j].dl {
		return h[i].dl < h[j].dl
	}
	return h[i].id < h[j].id
}

func (h inflightHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].flightIdx = i
	h[j].flightIdx = j
}

func (h *inflightHeap) Push(x any) {
	d := x.(*device)
	d.flightIdx = len(*h)
	*h = append(*h, d)
}

func (h *inflightHeap) Pop() any {
	old := *h
	n := len(old)
	d := old[n-1]
	old[n-1] = nil
	d.flightIdx = -1
	*h = old[:n-1]
	return d
}
