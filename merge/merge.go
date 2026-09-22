// Package merge performs K-way merging of sorted record runs using a heap.
package merge

import (
	"container/heap"
	"io"

	"ontology/record"
)

// Source yields the records of one run in sorted order; io.EOF ends it.
type Source interface {
	Next() (record.Record, error)
}

// SliceSource adapts an in-memory sorted slice to Source.
type SliceSource struct {
	recs []record.Record
	pos  int
}

// NewSliceSource returns a Source over recs.
func NewSliceSource(recs []record.Record) *SliceSource {
	return &SliceSource{recs: recs}
}

// Next implements Source.
func (s *SliceSource) Next() (record.Record, error) {
	if s.pos >= len(s.recs) {
		return record.Record{}, io.EOF
	}
	r := s.recs[s.pos]
	s.pos++
	return r, nil
}

type item struct {
	rec record.Record
	src int
}

type minHeap struct {
	items []item
	cmp   *int64
}

func (h *minHeap) Len() int { return len(h.items) }

func (h *minHeap) Less(i, j int) bool {
	*h.cmp++
	return record.Less(h.items[i].rec, h.items[j].rec)
}

func (h *minHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }

func (h *minHeap) Push(x any) { h.items = append(h.items, x.(item)) }

func (h *minHeap) Pop() any {
	old := h.items
	n := len(old)
	it := old[n-1]
	h.items = old[:n-1]
	return it
}

// Merger merges K sorted sources into one globally sorted stream.
type Merger struct {
	sources []Source
	cmp     int64 // unexported comparison counter
}

// New returns a Merger over the given sources.
func New(sources ...Source) *Merger { return &Merger{sources: sources} }

// Comparisons reports how many key comparisons the last Merge performed.
func (m *Merger) Comparisons() int64 { return m.cmp }

// Merge streams the merged records to emit in (Key, Seq) order.
func (m *Merger) Merge(emit func(record.Record) error) error {
	m.cmp = 0
	h := &minHeap{cmp: &m.cmp}
	for i, s := range m.sources {
		r, err := s.Next()
		if err == io.EOF {
			continue
		}
		if err != nil {
			return err
		}
		h.items = append(h.items, item{rec: r, src: i})
	}
	heap.Init(h)
	for h.Len() > 0 {
		top := heap.Pop(h).(item)
		if err := emit(top.rec); err != nil {
			return err
		}
		r, err := m.sources[top.src].Next()
		if err == io.EOF {
			continue
		}
		if err != nil {
			return err
		}
		heap.Push(h, item{rec: r, src: top.src})
	}
	return nil
}
