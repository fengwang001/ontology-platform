// Package merge performs a comparison-bounded K-way merge over sorted
// run sources using container/heap. Naively scanning every run per
// output record would cost O(K) comparisons per record; the heap uses
// O(log2 K), which the tests bound against 4*N*ceil(log2(K+1)).
package merge

import (
	"container/heap"
	"sync/atomic"

	"ontology/record"
)

// Merger merges sorted Sources into global (key, arrival) order.
type Merger struct {
	h     mergeHeap
	comps atomic.Uint64
}

// New builds a Merger from sorted sources.
func New(sources []Source) *Merger {
	m := &Merger{}
	m.h.comps = &m.comps
	for si := range sources {
		if len(sources[si].Records) > 0 {
			m.h.nodes = append(m.h.nodes, &node{src: &sources[si], pos: 0})
		}
	}
	heap.Init(&m.h)
	return m
}

// Next returns the next globally-smallest record and ok=true. At
// exhaustion ok is false.
func (m *Merger) Next() (record.Record, bool) {
	if len(m.h.nodes) == 0 {
		return record.Record{}, false
	}
	top := m.h.nodes[0]
	rec := top.src.Records[top.pos]
	top.pos++
	if top.pos < len(top.src.Records) {
		heap.Fix(&m.h, 0)
	} else {
		heap.Pop(&m.h)
	}
	return rec, true
}

// Comparisons reports the total key comparisons performed by the heap.
func (m *Merger) Comparisons() uint64 { return m.comps.Load() }

type node struct {
	src *Source
	pos int
}

type mergeHeap struct {
	nodes []*node
	comps *atomic.Uint64
}

func (h mergeHeap) Len() int { return len(h.nodes) }

func (h mergeHeap) Less(i, j int) bool {
	h.comps.Add(1)
	a := h.nodes[i].src.Records[h.nodes[i].pos]
	b := h.nodes[j].src.Records[h.nodes[j].pos]
	if a.Key != b.Key {
		return a.Key < b.Key
	}
	// Same key across runs: spill creation order is arrival order.
	return h.nodes[i].src.RunID < h.nodes[j].src.RunID
}

func (h mergeHeap) Swap(i, j int) { h.nodes[i], h.nodes[j] = h.nodes[j], h.nodes[i] }

func (h *mergeHeap) Push(x any) { h.nodes = append(h.nodes, x.(*node)) }

func (h *mergeHeap) Pop() any {
	old := h.nodes
	n := len(old)
	it := old[n-1]
	h.nodes = old[:n-1]
	return it
}
