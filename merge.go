package ontology

import (
	"container/heap"
	"math"
)

// Stats reports merge instrumentation.
type Stats struct {
	Comparisons int64 // element comparisons performed so far
	HeapLen     int   // elements currently inside the heap
	MaxHeapLen  int   // peak heap population during the merge
}

// Merger performs a single k-way merge over sorted streams.
// A Merger is single-use: each of Union, Intersect and Difference
// consumes the streams, so create a fresh Merger per operation.
type Merger struct {
	streams [][]float64
	pos     []int     // next unread index per stream
	prev    []float64 // last pulled value per stream
	pulled  []bool    // whether prev is valid per stream
	counts  []int     // per-stream occurrence counts of current group
	touched []int     // streams with nonzero counts in current group
	heap    *streamHeap
	stats   Stats
}

// NewMerger prepares a merge over the given sorted streams.
// The streams are not copied, modified or sorted.
func NewMerger(streams ...[]float64) *Merger {
	m := &Merger{
		streams: streams,
		pos:     make([]int, len(streams)),
		prev:    make([]float64, len(streams)),
		pulled:  make([]bool, len(streams)),
		counts:  make([]int, len(streams)),
	}
	m.heap = &streamHeap{
		less: func(a, b float64) bool {
			m.stats.Comparisons++
			return a < b
		},
	}
	return m
}

// Streams returns the number of input streams.
func (m *Merger) Streams() int { return len(m.streams) }

// Stats returns the merge counters. HeapLen never exceeds Streams()
// at any moment; after a finished merge it is 0.
func (m *Merger) Stats() Stats {
	m.stats.HeapLen = m.heap.Len()
	return m.stats
}

// advance validates and pushes the next element of stream s.
func (m *Merger) advance(s int) error {
	idx := m.pos[s]
	if idx >= len(m.streams[s]) {
		return nil
	}
	v := m.streams[s][idx]
	if math.IsNaN(v) {
		return NaNError{Stream: s, Index: idx}
	}
	if m.pulled[s] {
		m.stats.Comparisons++
		if m.prev[s] > v {
			return UnsortedError{Stream: s, Index: idx, Prev: m.prev[s], Cur: v}
		}
	}
	m.pulled[s] = true
	m.prev[s] = v
	m.pos[s] = idx + 1
	heap.Push(m.heap, item{value: v, stream: s})
	if n := m.heap.Len(); n > m.stats.MaxHeapLen {
		m.stats.MaxHeapLen = n
	}
	return nil
}

// run drives the merge, invoking emit once per distinct value with the
// per-stream occurrence counts of that value, in ascending order.
func (m *Merger) run(emit func(value float64, counts []int)) error {
	for s := range m.streams {
		if err := m.advance(s); err != nil {
			return err
		}
	}
	for m.heap.Len() > 0 {
		v := m.heap.peek().value
		m.touched = m.touched[:0]
		for m.heap.Len() > 0 {
			m.stats.Comparisons++
			if m.heap.peek().value != v {
				break
			}
			it := heap.Pop(m.heap).(item)
			m.counts[it.stream]++
			m.touched = append(m.touched, it.stream)
			if err := m.advance(it.stream); err != nil {
				return err
			}
		}
		emit(v, m.counts)
		for _, s := range m.touched {
			m.counts[s] = 0
		}
	}
	return nil
}

// compute runs the merge and appends each value repeat(counts) times.
func (m *Merger) compute(repeat func(counts []int) int) ([]float64, error) {
	out := []float64{}
	err := m.run(func(v float64, counts []int) {
		for i, n := 0, repeat(counts); i < n; i++ {
			out = append(out, v)
		}
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
