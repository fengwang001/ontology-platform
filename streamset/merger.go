package streamset

import "math"

// Stats reports merge progress and cost.
type Stats struct {
	// Comparisons is the number of heap ordering comparisons performed
	// so far. See the minHeap documentation for the linear upper bound.
	Comparisons int64
	// HeapSize is the number of elements currently in the merge heap.
	// It never exceeds the number of streams.
	HeapSize int
	// MaxHeapSize is the high-water mark of HeapSize so far.
	MaxHeapSize int
}

// Merger performs a single k-way merge over validated ascending streams,
// yielding each distinct value once together with its per-stream counts.
// At most one element per stream is in flight, so memory use is O(k).
type Merger struct {
	streams [][]float64
	heads   []int
	counts  []int
	heap    minHeap
	maxHeap int
}

// NewMerger validates every stream (rejecting NaN and non-ascending
// input) and primes the heap with each non-empty stream's first element.
func NewMerger(streams [][]float64) (*Merger, error) {
	for s, stream := range streams {
		for i, v := range stream {
			if math.IsNaN(v) {
				return nil, &NaNError{Stream: s, Index: i}
			}
			if i > 0 && stream[i-1] > v {
				return nil, &OrderError{Stream: s, Index: i}
			}
		}
	}
	m := &Merger{
		streams: streams,
		heads:   make([]int, len(streams)),
		counts:  make([]int, len(streams)),
	}
	for s, stream := range streams {
		if len(stream) > 0 {
			m.heap.push(heapItem{value: stream[0], stream: s})
		}
	}
	m.maxHeap = m.heap.len()
	return m, nil
}

// Next advances the merge and returns the next distinct value and its
// per-stream occurrence counts. ok is false when the merge is exhausted.
// The returned counts slice is owned by the Merger and reused across
// calls; copy it if it must outlive the next call.
func (m *Merger) Next() (value float64, counts []int, ok bool) {
	if m.heap.len() == 0 {
		return 0, nil, false
	}
	for i := range m.counts {
		m.counts[i] = 0
	}
	top := m.heap.pop()
	value = top.value
	m.counts[top.stream]++
	m.advance(top.stream)
	for m.heap.len() > 0 && m.heap.items[0].value == value {
		it := m.heap.pop()
		m.counts[it.stream]++
		m.advance(it.stream)
	}
	return value, m.counts, true
}

// advance moves one stream to its next element and pushes it if present.
func (m *Merger) advance(s int) {
	m.heads[s]++
	if m.heads[s] < len(m.streams[s]) {
		m.heap.push(heapItem{value: m.streams[s][m.heads[s]], stream: s})
		if m.heap.len() > m.maxHeap {
			m.maxHeap = m.heap.len()
		}
	}
}

// Stats returns the current merge statistics.
func (m *Merger) Stats() Stats {
	return Stats{
		Comparisons: m.heap.comparisons,
		HeapSize:    m.heap.len(),
		MaxHeapSize: m.maxHeap,
	}
}
