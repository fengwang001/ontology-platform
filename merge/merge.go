// Package merge merges validated CDC sources into one globally ordered change log with cross-source (Key,TS) deduplication.
package merge

import (
	"container/heap"
	"errors"
	"fmt"
	"math/bits"
	"ontology/msrc"
	"sync"
)

var ErrDupSource = errors.New("merge: duplicate source name")

type source struct {
	name string
	evs  []msrc.Event
	pos  int
}

func (s *source) head() msrc.Event { return s.evs[s.pos] }

// headHeap orders live sources by head ≺ (TS,Src,Seq); cmp counts comparisons so min-head location is O(log n).
type headHeap struct {
	s   []*source
	cmp *int
}

func (h *headHeap) Len() int      { return len(h.s) }
func (h *headHeap) Swap(i, j int) { h.s[i], h.s[j] = h.s[j], h.s[i] }
func (h *headHeap) Push(x any)    { h.s = append(h.s, x.(*source)) }
func (h *headHeap) Pop() any      { x := h.s[len(h.s)-1]; h.s = h.s[:len(h.s)-1]; return x }
func (h *headHeap) Less(i, j int) bool {
	*h.cmp++
	a, b := h.s[i].head(), h.s[j].head()
	return a.TS < b.TS || a.TS == b.TS && (a.Src < b.Src || a.Src == b.Src && a.Seq < b.Seq)
}

// Merger holds sources, the retained change log and the dup count.
type Merger struct {
	mu       sync.Mutex
	names    map[string]*source
	h        headHeap
	inited   bool
	log      []msrc.Event
	dups     int
	cmpCount int // unexported: only white-box tests read it; never exported
}

func New() *Merger { return &Merger{names: map[string]*source{}} }

// Add validates then registers; all checks precede mutation so a rejected call leaves no trace. Events are copied and name-stamped.
func (m *Merger) Add(name string, evs []msrc.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := msrc.Validate(name, evs); err != nil {
		return err
	}
	if _, ok := m.names[name]; ok {
		return ErrDupSource
	}
	cp := make([]msrc.Event, len(evs))
	for i, e := range evs {
		e.Src = name
		cp[i] = e
	}
	s := &source{name: name, evs: cp}
	m.names[name] = s
	if m.inited && len(cp) > 0 {
		heap.Push(&m.h, s)
	}
	return nil
}
func (m *Merger) ensureHeap() {
	if m.inited {
		return
	}
	bc := 0
	m.h = headHeap{cmp: &bc} // one-time O(n) build cost, not a watermark step
	for _, s := range m.names {
		if len(s.evs) > 0 {
			m.h.s = append(m.h.s, s)
		}
	}
	heap.Init(&m.h)
	m.h.cmp = &m.cmpCount
	m.inited = true
}

func (m *Merger) step() bool { // one watermark round: pop all heads at min TS W in ≺ order; the first event per Key wins, later same-(Key,TS) are dups
	m.ensureHeap()
	if m.h.Len() == 0 {
		return false
	}
	m.cmpCount = 0
	w := m.h.s[0].head().TS
	keys := map[string]struct{}{}
	for m.h.Len() > 0 && m.h.s[0].head().TS == w {
		s := heap.Pop(&m.h).(*source)
		e := s.head()
		if _, dup := keys[e.Key]; dup {
			m.dups++
		} else {
			keys[e.Key] = struct{}{}
			m.log = append(m.log, e)
		}
		s.pos++
		if s.pos < len(s.evs) {
			heap.Push(&m.h, s)
		}
	}
	return true
}
func (m *Merger) advance() {
	for m.step() {
	}
}
func (m *Merger) Drain() []msrc.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.advance()
	return append([]msrc.Event(nil), m.log...)
}
func (m *Merger) View() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.advance()
	v := make(map[string]string, len(m.log))
	for _, e := range m.log {
		v[e.Key] = e.Val
	}
	return v
}
func (m *Merger) Dups() int { m.mu.Lock(); defer m.mu.Unlock(); m.advance(); return m.dups }

func CheckMinHeadBound() error { // pass/fail only, never the count: within 4*log2(n)+4
	for _, n := range []int{100, 1000, 10000} {
		t := New()
		for i := 0; i < n; i++ {
			nm := fmt.Sprintf("s%05d", i)
			t.names[nm] = &source{name: nm, evs: []msrc.Event{{Src: nm, TS: int64(n - i), Key: "k"}}}
		}
		t.ensureHeap()
		t.step()
		if t.cmpCount > 4*bits.Len(uint(n))+4 {
			return fmt.Errorf("merge: %d comparisons exceed bound at n=%d", t.cmpCount, n)
		}
	}
	return nil
}
