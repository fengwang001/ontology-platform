package ontology

import "math"

// normalize canonicalizes a value for grouping and output: -0.0 becomes
// +0.0 so the two signed zeros are the same value. NaN must have been
// rejected before this is called.
func normalize(v float64) float64 {
	if v == 0 {
		return 0
	}
	return v
}

// merger performs one k-way merge over the input streams. At any moment
// the heap holds at most one element per stream, so extra memory is
// O(number of streams) regardless of stream lengths.
type merger struct {
	streams [][]float64
	next    []int // per-stream index of the next element to push
	heap    minHeap
	maxHeap int
}

// newMerger validates every stream (rejecting NaN and strictly
// decreasing pairs) and seeds the heap with each stream's head.
// Validation is a plain O(n) scan; it is not a merge and does not count
// toward Stats.Comparisons.
func newMerger(streams [][]float64) (*merger, error) {
	m := &merger{streams: streams, next: make([]int, len(streams))}
	for s, stream := range streams {
		prev := 0.0
		for i, raw := range stream {
			if math.IsNaN(raw) {
				return nil, &NaNError{Stream: s, Index: i}
			}
			v := normalize(raw)
			if i > 0 && prev > v {
				return nil, &OrderError{Stream: s, Index: i}
			}
			prev = v
		}
		if len(stream) > 0 {
			m.heap.push(heapItem{value: normalize(stream[0]), stream: s})
			m.next[s] = 1
		}
	}
	m.maxHeap = m.heap.len()
	return m, nil
}

// pop removes the smallest in-flight element and refills the heap from
// the same stream. ok is false once every stream is exhausted.
func (m *merger) pop() (it heapItem, ok bool) {
	if m.heap.len() == 0 {
		return heapItem{}, false
	}
	it = m.heap.pop()
	s := it.stream
	if m.next[s] < len(m.streams[s]) {
		m.heap.push(heapItem{value: normalize(m.streams[s][m.next[s]]), stream: s})
		m.next[s]++
		if m.heap.len() > m.maxHeap {
			m.maxHeap = m.heap.len()
		}
	}
	return it, true
}

// stats reports how many comparisons the merge has performed so far and
// how many elements the heap currently holds (never more than the number
// of streams).
func (m *merger) stats() (comparisons, heapSize int) {
	return m.heap.comparisons, m.heap.len()
}
