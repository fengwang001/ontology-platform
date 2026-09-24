// Package aqueue implements the ordered and segmented-unordered buffers.
package aqueue

import "strconv"
import "ontology/aentry"

// base holds shared state plus hooks for each mode's release policy; all validation precedes any state change.
type base struct {
	capN    int
	index   map[string]*aentry.Entry // every accepted element, forever
	occ     int                      // accepted, not yet output
	seq     aentry.Sequencer
	lastT   int64
	hasWM   bool
	checked int // entries examined by the most recent Complete
	onPush  func(e *aentry.Entry)
	rel     func(done *aentry.Entry) (out []aentry.Entry, looked int)
}

func (b *base) In(id string) ([]aentry.Entry, error) {
	if b.occ >= b.capN {
		return nil, aentry.ErrFull
	}
	if err := aentry.CheckID(id, func(s string) bool { _, ok := b.index[s]; return ok }); err != nil {
		return nil, err
	}
	b.index[id] = &aentry.Entry{Kind: aentry.Element, ID: id}
	b.occ++
	b.onPush(b.index[id])
	return nil, nil
}
func (b *base) Watermark(t int64) ([]aentry.Entry, error) {
	if err := aentry.CheckWatermark(t, b.lastT, b.hasWM); err != nil {
		return nil, err
	}
	b.lastT, b.hasWM = t, true
	b.onPush(&aentry.Entry{Kind: aentry.Watermark, T: t})
	out, _ := b.rel(nil)
	return out, nil
}
func (b *base) Complete(id string) ([]aentry.Entry, error) {
	e, ok := b.index[id]
	if !ok || e.Done {
		return nil, aentry.ErrUnknown
	}
	e.Done, e.Seq = true, b.seq.Next()
	out, n := b.rel(e)
	b.checked = n
	return out, nil
}
func (b *base) Occupancy() int { return b.occ } // accepted, not yet output

// NewOrdered returns a queue whose output is a strict prefix of the input (head-of-line blocking).
func NewOrdered(capN int) *base {
	var q []*aentry.Entry
	b := &base{capN: capN, index: map[string]*aentry.Entry{}}
	b.onPush = func(e *aentry.Entry) { q = append(q, e) }
	b.rel = func(*aentry.Entry) ([]aentry.Entry, int) {
		var out []aentry.Entry
		n := 0
		for len(q) > 0 {
			n++
			h := q[0]
			if h.Kind == aentry.Element && !h.Done {
				break
			}
			out = append(out, *h)
			q = q[1:]
			if h.Kind == aentry.Element {
				b.occ--
			}
		}
		return out, n
	}
	return b
}

// NewUnordered returns a segmented queue: open segments emit on completion, the rest is held behind the watermark barrier and released in completion order, cascading.
func NewUnordered(capN int) *base {
	type segment struct {
		elems map[string]*aentry.Entry // not yet output
		held  []string                 // done ids in completion order (unopened)
		wm    *aentry.Entry            // closing watermark, nil until it arrives
	}
	segs := []*segment{{elems: map[string]*aentry.Entry{}}}
	front, segOf := 0, map[string]int{} // front: first not-fully-drained segment (segments <= front are open)
	b := &base{capN: capN, index: map[string]*aentry.Entry{}}
	remove := func(id string, s *segment) { delete(s.elems, id); delete(segOf, id); b.occ-- }
	b.onPush = func(e *aentry.Entry) {
		last := segs[len(segs)-1]
		if e.Kind == aentry.Watermark {
			last.wm = e
			segs = append(segs, &segment{elems: map[string]*aentry.Entry{}})
			return
		}
		last.elems[e.ID] = e
		segOf[e.ID] = len(segs) - 1
	}
	b.rel = func(done *aentry.Entry) ([]aentry.Entry, int) {
		var out []aentry.Entry
		n := 0
		if done != nil { // a completion: emit if its segment is open, else hold it
			n = 1
			if si := segOf[done.ID]; si > front {
				segs[si].held = append(segs[si].held, done.ID)
			} else {
				remove(done.ID, segs[si])
				out = append(out, *done)
			}
		}
		for front < len(segs) { // cascade: drain segments whose elements are all out
			n++
			s := segs[front]
			if s.wm == nil || len(s.elems) > 0 {
				break
			}
			out = append(out, *s.wm)
			front++
			if front == len(segs) {
				break
			}
			nx := segs[front]
			for _, id := range nx.held { // newly opened: release in completion order
				n++
				out = append(out, *nx.elems[id])
				remove(id, nx)
			}
			nx.held = nil
		}
		return out, n
	}
	return b
}

// SelfCheckBounded reports whether a Complete on a non-head element examines O(1)+outputs entries at several sizes.
func SelfCheckBounded() bool {
	bounded := true
	for _, m := range []int{100, 1000, 10000} {
		for _, q := range []*base{NewOrdered(m + 1), NewUnordered(m + 1)} {
			for i := 0; i < m; i++ {
				_, err := q.In("e" + strconv.Itoa(i))
				bounded = bounded && err == nil
			}
			out, err := q.Complete("e" + strconv.Itoa(m-1))
			bounded = bounded && err == nil && q.checked <= len(out)+3
		}
	}
	return bounded
}
