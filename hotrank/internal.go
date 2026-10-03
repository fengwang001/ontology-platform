package hotrank

import "container/heap"

// int64Heap is a min-heap of bucket numbers that currently have data.
// Expired buckets are popped from the root, so Add never scans the window.
type int64Heap []int64

func (h int64Heap) Len() int           { return len(h) }
func (h int64Heap) Less(i, j int) bool { return h[i] < h[j] }
func (h int64Heap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *int64Heap) Push(x any) {
	*h = append(*h, x.(int64))
}

func (h *int64Heap) Pop() any {
	old := *h
	n := len(old)
	v := old[n-1]
	*h = old[:n-1]
	return v
}

// collectExpired pops bucket numbers <= limit from the heap. It only touches
// the heap; bucket/score maps are modified later in commitExpired, so a caller
// that rejects the operation afterwards can push the numbers back.
func (b *Board) collectExpired(limit int64) []int64 {
	var expired []int64
	for b.bucketOrder.Len() > 0 && (*b.bucketOrder)[0] <= limit {
		expired = append(expired, heap.Pop(b.bucketOrder).(int64))
	}
	return expired
}

// commitExpired subtracts expired bucket totals from per-id window scores and
// deletes the bucket maps. Every inner (bucket, id) pair touched increments
// addExpireScans, proving Add only visits buckets that actually leave the
// window rather than scanning all W buckets.
func (b *Board) commitExpired(expired []int64) {
	for _, bn := range expired {
		m := b.buckets[bn]
		for id, v := range m {
			b.scores[id] -= v
			b.addExpireScans++
			if b.scores[id] == 0 {
				if _, onPrev := b.prevRank[id]; !onPrev {
					delete(b.scores, id)
				}
			}
		}
		delete(b.buckets, bn)
	}
}

func (b *Board) restoreExpired(expired []int64) {
	for i := len(expired) - 1; i >= 0; i-- {
		heap.Push(b.bucketOrder, expired[i])
	}
}
