package thinpool

import "container/heap"

type physicalAllocator struct {
	physicalBlocks uint64
	allocated      uint64
	nextBlock      uint64
	freeBlocks     *uint64Heap
}

func newPhysicalAllocator(physicalBlocks uint64) *physicalAllocator {
	return &physicalAllocator{
		physicalBlocks: physicalBlocks,
		freeBlocks:     &uint64Heap{},
	}
}

func (a *physicalAllocator) allocate() (uint64, bool) {
	if a.allocated >= a.physicalBlocks {
		return 0, false
	}

	var block uint64
	if a.freeBlocks.Len() > 0 {
		block = heap.Pop(a.freeBlocks).(uint64)
	} else {
		block = a.nextBlock
		a.nextBlock++
	}
	a.allocated++
	return block, true
}

func (a *physicalAllocator) free(block uint64) {
	heap.Push(a.freeBlocks, block)
	a.allocated--
}

type uint64Heap []uint64

func (h uint64Heap) Len() int {
	return len(h)
}

func (h uint64Heap) Less(i, j int) bool {
	return h[i] < h[j]
}

func (h uint64Heap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *uint64Heap) Push(value any) {
	*h = append(*h, value.(uint64))
}

func (h *uint64Heap) Pop() any {
	old := *h
	last := len(old) - 1
	value := old[last]
	*h = old[:last]
	return value
}
