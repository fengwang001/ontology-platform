package deviceflow

type expiryEntry struct {
	expiresAt int64
	seq       int64
	auth      *auth
}

type expiryHeap []expiryEntry

func (h expiryHeap) Len() int { return len(h) }

func (h expiryHeap) Less(i, j int) bool {
	if h[i].expiresAt != h[j].expiresAt {
		return h[i].expiresAt < h[j].expiresAt
	}
	return h[i].seq < h[j].seq
}

func (h expiryHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *expiryHeap) Push(x any) { *h = append(*h, x.(expiryEntry)) }

func (h *expiryHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = expiryEntry{}
	*h = old[:n-1]
	return e
}
