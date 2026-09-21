package ontology

import "container/heap"

// Merger performs a single k-way merge over sorted float64 streams.
// At any moment it holds at most one cursor per stream inside a
// min-heap, so its extra memory is O(number of streams).
type Merger struct {
	streams [][]float64
	pos     []int
	h       streamHeap
	cmp     int64
}

// NewMerger validates the streams and returns a ready-to-use Merger.
// The streams are not copied and never modified.
func NewMerger(streams [][]float64) (*Merger, error) {
	for i, s := range streams {
		if err := validateStream(i, s); err != nil {
			return nil, err
		}
	}
	m := &Merger{
		streams: streams,
		pos:     make([]int, len(streams)),
	}
	m.h.less = m.headLess
	for i := range streams {
		if len(streams[i]) > 0 {
			m.h.idx = append(m.h.idx, i)
		}
	}
	heap.Init(&m.h)
	return m, nil
}

// headLess reports whether the current head of stream a is strictly
// less than the current head of stream b. +0.0 and -0.0 compare equal.
// Every call counts as one comparison.
func (m *Merger) headLess(a, b int) bool {
	m.cmp++
	return m.streams[a][m.pos[a]] < m.streams[b][m.pos[b]]
}

// Comparisons returns the number of head comparisons performed so far.
// For k streams and N total elements it is bounded by
// N*(2*ceil(log2(k))+2)+2k: each element triggers at most one sift-down
// (at most 2*ceil(log2(k)) comparisons) plus one equality check, and
// heap.Init costs at most 2k.
func (m *Merger) Comparisons() int64 { return m.cmp }

// HeapLen returns the number of stream heads currently in the heap.
// It never exceeds the number of streams.
func (m *Merger) HeapLen() int { return m.h.Len() }

// head returns the current head value of stream i.
func (m *Merger) head(i int) float64 { return m.streams[i][m.pos[i]] }

// Next advances the merge and returns the next distinct value together
// with its occurrence count in each stream. ok is false when every
// stream is exhausted. The returned counts slice is freshly allocated.
func (m *Merger) Next() (value float64, counts []int, ok bool) {
	if m.h.Len() == 0 {
		return 0, nil, false
	}
	counts = make([]int, len(m.streams))
	value = m.head(m.h.top())
	for m.h.Len() > 0 {
		t := m.h.top()
		m.cmp++ // equality check against the current group value
		if m.head(t) != value {
			break
		}
		counts[t]++
		m.pos[t]++
		if m.pos[t] < len(m.streams[t]) {
			heap.Fix(&m.h, 0)
		} else {
			heap.Pop(&m.h)
		}
	}
	return value, counts, true
}
