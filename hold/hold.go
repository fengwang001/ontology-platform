package hold

import "container/heap"

type Record struct {
	Dev     int64
	Boot    int64
	K       int64
	Payload any
}

type entry struct {
	record Record
	order  uint64
}

type minHeap []entry

func (h minHeap) Len() int {
	return len(h)
}

func (h minHeap) Less(i, j int) bool {
	if h[i].record.K != h[j].record.K {
		return h[i].record.K < h[j].record.K
	}
	return h[i].order < h[j].order
}

func (h minHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *minHeap) Push(x any) {
	*h = append(*h, x.(entry))
}

func (h *minHeap) Pop() any {
	old := *h
	last := old[len(old)-1]
	*h = old[:len(old)-1]
	return last
}

type Buffer struct {
	records  minHeap
	next     uint64
	maxK     int64
	examined int
}

func NewBuffer() *Buffer {
	return &Buffer{}
}

func (b *Buffer) Add(r Record) {
	if len(b.records) == 0 || r.K > b.maxK {
		b.maxK = r.K
	}
	heap.Push(&b.records, entry{record: r, order: b.next})
	b.next++
}

func (b *Buffer) ReleaseLE(k int64) []Record {
	b.examined = 0
	released := []Record{}
	for len(b.records) > 0 {
		b.examined++
		if b.records[0].record.K > k {
			break
		}
		released = append(released, heap.Pop(&b.records).(entry).record)
	}
	return released
}

func (b *Buffer) ReleaseAll() []Record {
	released := make([]Record, 0, len(b.records))
	for len(b.records) > 0 {
		released = append(released, heap.Pop(&b.records).(entry).record)
	}
	return released
}

func (b *Buffer) Len() int {
	return len(b.records)
}

func (b *Buffer) MaxK() (int64, bool) {
	if len(b.records) == 0 {
		return 0, false
	}
	return b.maxK, true
}

func (b *Buffer) Examined() int {
	return b.examined
}
