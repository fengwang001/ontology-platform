// Package topk keeps the best k elements with a worst-first heap of the
// top-k and a best-first heap of the rest. Depends only on package ord.
package topk

import (
	"errors"
	"fmt"
	"math/bits"
	"ontology/ord"
	"sort"
)

var ErrComplexity = errors.New("topk: heap ops exceeded O(log n) bound") // verdict only; counter unexported
type entry struct {
	e   ord.Elem
	ver int64
}
type current struct{ score, ver int64 }

// rankLess orders by element order; copies of one id tie-break older first.
func rankLess(a, b entry) bool {
	return ord.Less(a.e, b.e) || (!ord.Less(b.e, a.e) && a.ver < b.ver)
}

// Maintainer is the dual-heap structure; not concurrency-safe (api locks).
type Maintainer struct {
	k, capN int
	gen     int64              // globally monotone copy version, never reused
	cur     map[string]current // id -> current (score, version)
	side    map[string]bool    // live id owns a top seat iff true
	top     *ord.Heap[entry]
	bot     *ord.Heap[entry]
	inTop   int
	ops     int64 // compares/swaps of the last Add/Remove (unexported)
}

func New(k, capN int) *Maintainer {
	m := &Maintainer{k: k, capN: capN, cur: map[string]current{}, side: map[string]bool{}}
	count := func(n int) { m.ops += int64(n) }
	m.top = ord.NewHeap[entry](func(a, b entry) bool { return rankLess(b, a) }, count)
	m.bot = ord.NewHeap[entry](rankLess, count)
	return m
}
func (m *Maintainer) Has(id string) bool { _, ok := m.cur[id]; return ok }
func (m *Maintainer) Count() int         { return len(m.cur) }

// Add inserts e or replaces its score (upsert), then rebalances.
func (m *Maintainer) Add(e ord.Elem) {
	m.ops = 0
	_, existed := m.cur[e.ID]
	m.gen++
	en := entry{e: e, ver: m.gen}
	m.cur[e.ID] = current{e.Score, en.ver}
	h := m.bot
	switch {
	case !existed && m.Count() <= m.k: // filling the first k seats
		h, m.side[e.ID], m.inTop = m.top, true, m.inTop+1
	case !existed:
		m.side[e.ID] = false
	case m.side[e.ID]:
		h = m.top // prior copy was in top and is now stale
	}
	h.Push(en)
	m.rebalance()
}

// Remove deletes id; a missing id is an idempotent no-op.
func (m *Maintainer) Remove(id string) {
	m.ops = 0
	if _, ok := m.cur[id]; !ok {
		return
	}
	if m.side[id] {
		m.inTop--
	}
	delete(m.cur, id)
	delete(m.side, id) // stale heap copies are reclaimed lazily
	m.rebalance()
}

// Tops returns the live top-k, best first.
func (m *Maintainer) Tops() []ord.Elem {
	out := make([]ord.Elem, 0, m.k)
	for _, en := range m.top.Snapshot() {
		if c, ok := m.cur[en.e.ID]; ok && c.ver == en.ver {
			out = append(out, en.e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return ord.Less(out[i], out[j]) })
	return out[:min(len(out), m.k)]
}
func (m *Maintainer) purge(h *ord.Heap[entry]) {
	// inTop is unchanged: a superseded copy's seat stays owned by its new version.
	for h.Len() > 0 {
		en := h.Peek()
		if c, ok := m.cur[en.e.ID]; ok && c.ver == en.ver {
			return
		}
		h.Pop()
	}
}
func (m *Maintainer) rebalance() {
	// Restore the partition: top = best min(k,len(cur)) current elements.
	m.purge(m.top)
	m.purge(m.bot)
	for m.inTop > 0 && m.bot.Len() > 0 && rankLess(m.bot.Peek(), m.top.Peek()) {
		up, down := m.bot.Pop(), m.top.Pop() // best below climbs; worst drops
		m.side[up.e.ID], m.side[down.e.ID] = true, false
		m.top.Push(up)
		m.bot.Push(down)
		m.purge(m.top)
		m.purge(m.bot)
	}
	want := min(m.k, len(m.cur))
	for m.inTop > want { // filling-phase overflow: spill the worst
		m.purge(m.top)
		e := m.top.Pop()
		m.bot.Push(e)
		m.side[e.e.ID] = false
		m.inTop--
	}
	for m.inTop < want { // backfill after an in-top removal
		m.purge(m.bot)
		if m.bot.Len() == 0 {
			break
		}
		e := m.bot.Pop()
		m.top.Push(e)
		m.side[e.e.ID] = true
		m.inTop++
	}
}
func logBound(n int) int64 { return 8 * int64(1+bits.Len(uint(n))) }

// ComplexityCheck inserts n at several scales then one below-threshold
// element, failing unless the (unexported) heap-op count is O(log n) and
// strictly below n. Only the verdict is exposed.
func ComplexityCheck() error {
	for _, n := range []int{100, 1000, 10000} {
		m := New(10, n+10)
		for i := 0; i < n; i++ {
			m.Add(ord.Elem{ID: fmt.Sprintf("x%08d", i), Score: int64(i)})
		}
		m.Add(ord.Elem{ID: "below", Score: -1})
		if m.ops >= int64(n) || m.ops > logBound(n) {
			return fmt.Errorf("%w: n=%d", ErrComplexity, n)
		}
	}
	return nil
}
