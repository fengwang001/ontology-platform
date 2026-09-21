package ontology

// Merger performs a single k-way merge over the input streams, yielding
// each distinct value once together with its per-stream multiplicities.
//
// Memory: the merger keeps exactly one cursor and at most one in-flight
// heap element per stream, plus one counts slot per stream. Extra memory
// is therefore O(k) in the number of streams k and independent of the
// total stream length N.
//
// Comparisons: each of the N elements is pushed once (at most ceil(log2 k)
// sift-up comparisons) and popped once (at most 2*ceil(log2 k) sift-down
// comparisons), and each element participates in at most two equality
// checks against the current group. Hence the total number of comparisons
// C is linear in N:
//
//	C <= N * (3*ceil(log2(k)) + 2)   for k >= 1, and C = 0 for k = 0.
//
// This bound is exposed to tests via ComparisonBound.
type Merger struct {
	streams [][]float64
	pos     []int
	heap    minHeap
	cmp     int64
	counts  []int
}

// NewMerger validates the streams and seeds the heap with the first
// element of each non-empty stream. The input slices are never modified.
func NewMerger(streams [][]float64) (*Merger, error) {
	if err := validate(streams); err != nil {
		return nil, err
	}
	m := &Merger{
		streams: streams,
		pos:     make([]int, len(streams)),
		counts:  make([]int, len(streams)),
	}
	m.heap.cmp = &m.cmp
	for s := range streams {
		m.advance(s)
	}
	return m, nil
}

// ComparisonBound returns the documented linear upper bound on the number
// of comparisons a merge of k streams with N total elements can perform.
func ComparisonBound(totalElements, numStreams int64) int64 {
	if numStreams <= 0 || totalElements <= 0 {
		return 0
	}
	log2k := int64(0)
	for n := int64(1); n < numStreams; n <<= 1 {
		log2k++ // ceil(log2(k))
	}
	return totalElements * (3*log2k + 2)
}

// Comparisons returns how many comparisons the merge has performed so far.
func (m *Merger) Comparisons() int64 { return m.cmp }

// HeapSize returns the number of elements currently inside the merge heap.
// It never exceeds the number of streams.
func (m *Merger) HeapSize() int { return m.heap.len() }

// Streams returns how many streams are being merged.
func (m *Merger) Streams() int { return len(m.streams) }

// advance moves stream s's cursor one step forward, pushing the newly
// exposed element (if any) onto the heap.
func (m *Merger) advance(s int) {
	if m.pos[s] < len(m.streams[s]) {
		m.heap.push(heapItem{val: m.streams[s][m.pos[s]], stream: s})
		m.pos[s]++
	}
}

// Next emits the next distinct value in merged order. counts[i] reports
// how many times the value occurs in stream i. ok is false once every
// stream is exhausted.
//
// The returned counts slice is owned by the Merger and is overwritten by
// the next call to Next; copy it if it must outlive the call.
func (m *Merger) Next() (val float64, counts []int, ok bool) {
	if m.heap.len() == 0 {
		return 0, nil, false
	}
	first := m.heap.pop()
	val = first.val
	clear(m.counts)
	m.counts[first.stream]++
	m.advance(first.stream)
	// Drain every heap entry equal to the group value. "==" treats +0.0
	// and -0.0 as equal, which is exactly the required contract; NaN can
	// never appear because validation rejected it.
	for m.heap.len() > 0 {
		top := m.heap.peek()
		m.cmp++
		if top.val != val {
			break
		}
		it := m.heap.pop()
		m.counts[it.stream]++
		m.advance(it.stream)
	}
	return val, m.counts, true
}
