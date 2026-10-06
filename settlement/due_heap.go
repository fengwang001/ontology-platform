package settlement

type dueMinHeap []int

func (heap dueMinHeap) Len() int           { return len(heap) }
func (heap dueMinHeap) Less(i, j int) bool { return heap[i] < heap[j] }
func (heap dueMinHeap) Swap(i, j int)      { heap[i], heap[j] = heap[j], heap[i] }

func (heap *dueMinHeap) Push(value any) {
	*heap = append(*heap, value.(int))
}

func (heap *dueMinHeap) Pop() any {
	old := *heap
	value := old[len(old)-1]
	*heap = old[:len(old)-1]
	return value
}
