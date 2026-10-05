package cpm

type candidate struct {
	value int64
	index int
	alive bool
	edge  bool
	src   int
	dst   int
	kind  int
}

const (
	kindEdge = iota
	kindStart
	kindDeadline
	kindFinish
)

type maxHeap []*candidate

func (h maxHeap) Len() int           { return len(h) }
func (h maxHeap) Less(i, j int) bool { return h[i].value > h[j].value }
func (h maxHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i]; h[i].index = i; h[j].index = j }
func (h *maxHeap) Push(x any)        { c := x.(*candidate); c.index = len(*h); *h = append(*h, c) }
func (h *maxHeap) Pop() any {
	old := *h
	n := len(old)
	c := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	c.index = -1
	return c
}

type minHeap []*candidate

func (h minHeap) Len() int           { return len(h) }
func (h minHeap) Less(i, j int) bool { return h[i].value < h[j].value }
func (h minHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i]; h[i].index = i; h[j].index = j }
func (h *minHeap) Push(x any)        { c := x.(*candidate); c.index = len(*h); *h = append(*h, c) }
func (h *minHeap) Pop() any {
	old := *h
	n := len(old)
	c := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	c.index = -1
	return c
}

type valueItem struct {
	task  int
	value int64
	index int
}

type valueMaxHeap []*valueItem

func (h valueMaxHeap) Len() int           { return len(h) }
func (h valueMaxHeap) Less(i, j int) bool { return h[i].value > h[j].value }
func (h valueMaxHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i]; h[i].index = i; h[j].index = j }
func (h *valueMaxHeap) Push(x any) {
	item := x.(*valueItem)
	item.index = len(*h)
	*h = append(*h, item)
}
func (h *valueMaxHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	item.index = -1
	return item
}

type valueMinHeap []*valueItem

func (h valueMinHeap) Len() int { return len(h) }
func (h valueMinHeap) Less(i, j int) bool {
	return h[i].value < h[j].value || (h[i].value == h[j].value && h[i].task < h[j].task)
}
func (h valueMinHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i]; h[i].index = i; h[j].index = j }
func (h *valueMinHeap) Push(x any) {
	item := x.(*valueItem)
	item.index = len(*h)
	*h = append(*h, item)
}
func (h *valueMinHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	item.index = -1
	return item
}
