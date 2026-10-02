package watermark

type wsHeap []int64

func (h wsHeap) Len() int           { return len(h) }
func (h wsHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h wsHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *wsHeap) Push(x any)        { *h = append(*h, x.(int64)) }
func (h *wsHeap) Pop() any {
	old := *h
	n := len(old)
	v := old[n-1]
	*h = old[:n-1]
	return v
}

type holdEntry struct {
	hold int64
	ws   int64
}

type holdHeap []holdEntry

func (h holdHeap) Len() int { return len(h) }
func (h holdHeap) Less(i, j int) bool {
	if h[i].hold != h[j].hold {
		return h[i].hold < h[j].hold
	}
	return h[i].ws < h[j].ws
}
func (h holdHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *holdHeap) Push(x any)   { *h = append(*h, x.(holdEntry)) }
func (h *holdHeap) Pop() any {
	old := *h
	n := len(old)
	v := old[n-1]
	*h = old[:n-1]
	return v
}
