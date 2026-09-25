// Package merge merges multiple validated CDC sources into one totally
// ordered change log with cross-source dedup.
package merge

import (
	"container/heap"
	"errors"
	"sort"

	"ontology/msrc"
)

var ErrDuplicateName = errors.New("merge: duplicate source name")

// less is the global total order ≺: TS asc, then Src asc, then Seq asc.
func less(a, b msrc.Event) bool {
	if a.TS != b.TS {
		return a.TS < b.TS
	}
	if a.Src != b.Src {
		return a.Src < b.Src
	}
	return a.Seq < b.Seq
}

type source struct {
	evs []msrc.Event
	pos int
}

func (s *source) head() msrc.Event { return s.evs[s.pos] }

// srcHeap is a min-heap of sources ordered by their head event.
type srcHeap struct {
	s    []*source
	cmps *int // counts head comparisons; wired to Merger.cmps
}

func (h srcHeap) Len() int { return len(h.s) }
func (h srcHeap) Less(i, j int) bool {
	*h.cmps++
	return less(h.s[i].head(), h.s[j].head())
}
func (h srcHeap) Swap(i, j int) { h.s[i], h.s[j] = h.s[j], h.s[i] }
func (h *srcHeap) Push(x any)   { h.s = append(h.s, x.(*source)) }
func (h *srcHeap) Pop() any {
	old := h.s
	x := old[len(old)-1]
	h.s = old[:len(old)-1]
	return x
}

// Merger holds per-source heads, the watermark heap, log and dedup counter.
type Merger struct {
	names  map[string]struct{}
	h      srcHeap
	inited bool
	cmps   int // head comparisons of the most recent pickMin; unexported per spec
	log    []msrc.Event
	dups   int
}

func New() *Merger {
	m := &Merger{names: map[string]struct{}{}}
	m.h.cmps = &m.cmps
	return m
}

// AddSource validates evs and registers the source. All checks run before
// any state changes, so a rejected call leaves no trace.
func (m *Merger) AddSource(name string, evs []msrc.Event) error {
	if _, ok := m.names[name]; ok {
		return ErrDuplicateName
	}
	if err := msrc.Validate(evs); err != nil {
		return err
	}
	cp := make([]msrc.Event, len(evs))
	for i, e := range evs {
		e.Src = name
		cp[i] = e
	}
	m.names[name] = struct{}{}
	m.h.s = append(m.h.s, &source{evs: cp})
	return nil
}

func (m *Merger) ensureInit() {
	if !m.inited {
		heap.Init(&m.h)
		m.cmps = 0 // heap construction is not a pickMin
		m.inited = true
	}
}

// pickMin removes the ≺-smallest head source; cmps measures this operation.
func (m *Merger) pickMin() *source {
	m.cmps = 0
	return heap.Pop(&m.h).(*source)
}

// Step processes one watermark batch: every source whose head.TS equals the
// watermark W pops one event; per (Key, TS) only the ≺-smallest is kept,
// the rest are counted as dups; kept events are appended in ≺ order.
// Returns false when all sources are drained.
func (m *Merger) Step() bool {
	m.ensureInit()
	if m.h.Len() == 0 {
		return false
	}
	w := m.h.s[0].head().TS
	var batch []*source
	for m.h.Len() > 0 && m.h.s[0].head().TS == w {
		batch = append(batch, m.pickMin())
	}
	best := make(map[string]msrc.Event, len(batch))
	for _, s := range batch {
		if e := s.head(); best[e.Key].Key == "" || less(e, best[e.Key]) {
			best[e.Key] = e
		}
	}
	kept := make([]msrc.Event, 0, len(best))
	for _, s := range batch {
		if e := s.head(); best[e.Key] == e {
			kept = append(kept, e)
		} else {
			m.dups++
		}
	}
	sort.Slice(kept, func(i, j int) bool { return less(kept[i], kept[j]) })
	m.log = append(m.log, kept...)
	for _, s := range batch {
		if s.pos++; s.pos < len(s.evs) {
			heap.Push(&m.h, s)
		}
	}
	return true
}

// Run drains all sources.
func (m *Merger) Run() {
	for m.Step() {
	}
}

// Log returns a copy of the merged change log so far.
func (m *Merger) Log() []msrc.Event { return append([]msrc.Event(nil), m.log...) }

// Dups returns the number of discarded duplicate events.
func (m *Merger) Dups() int { return m.dups }
