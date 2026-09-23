// Package wheel implements a multi-level hierarchical timing wheel with an
// overflow min-heap. Depends only on the slot package.
package wheel

import (
	"container/heap"
	"errors"
	"sort"

	"ontology/slot"
)

// ErrConfig is returned for invalid wheel parameters.
var ErrConfig = errors.New("wheel: invalid configuration")

// Node is a wheel entry; it aliases the slot list node.
type Node = slot.Node

// Fire is invoked once per due node in (deadline, seq) order. Re-added
// nodes never become due within the same Advance call.
type Fire func(n *Node)

type oh []*Node

func (h oh) Len() int           { return len(h) }
func (h oh) Less(i, j int) bool { return h[i].Deadline < h[j].Deadline }
func (h oh) Swap(i, j int)      { h[i], h[j] = h[j], h[i]; h[i].Pos, h[j].Pos = i, j }
func (h *oh) Push(x any)        { n := x.(*Node); n.Pos = len(*h); *h = append(*h, n) }
func (h *oh) Pop() any {
	old := *h
	n := old[len(old)-1]
	*h = old[:len(old)-1]
	return n
}

// Wheel is a set of slot rings plus an overflow min-heap. Ticks are the
// caller's unit; cur is the current absolute tick index.
type Wheel struct {
	cur      int64
	w, lv    int
	rings    []*slot.Ring
	base     []int64
	nonempty [][]int
	imm      []*Node
	ovf      *oh
	visits   int
}

// New creates a wheel: slots per level (power of two), levels >= 1, cur0
// the initial tick index.
func New(slots, levels int, cur0 int64) (*Wheel, error) {
	if levels <= 0 {
		return nil, ErrConfig
	}
	wh := &Wheel{cur: cur0, w: slots, lv: levels}
	wh.rings = make([]*slot.Ring, levels)
	wh.base = make([]int64, levels)
	wh.nonempty = make([][]int, levels)
	for i := range wh.rings {
		r, err := slot.New(slots)
		if err != nil {
			return nil, err
		}
		wh.rings[i] = r
	}
	o := &oh{}
	heap.Init(o)
	wh.ovf = o
	return wh, nil
}

// Visits reports slots drained during the most recent Advance.
func (wh *Wheel) Visits() int { return wh.visits }

// Cur reports the current tick index.
func (wh *Wheel) Cur() int64 { return wh.cur }

// Add inserts a node with absolute deadline tick d and order key seq.
func (wh *Wheel) Add(n *Node, d, seq int64) {
	n.Deadline, n.Seq = d, seq
	if d <= wh.cur {
		n.Level = -1
		n.Pos = len(wh.imm)
		wh.imm = append(wh.imm, n)
		return
	}
	wh.place(n, d)
}

// place assigns a node to a ring level/slot or the overflow heap.
func (wh *Wheel) place(n *Node, d int64) {
	dist := d - wh.cur
	span := int64(wh.w)
	for l := 0; l < wh.lv; l++ {
		if dist < span {
			s := int(d / span)
			wh.rings[l].PushBack(n, s)
			n.Level = l
			wh.mark(l, s)
			return
		}
		span *= int64(wh.w)
	}
	n.Level = wh.lv
	heap.Push(wh.ovf, n)
}

func (wh *Wheel) mark(l, s int) {
	v := wh.nonempty[l]
	i := sort.SearchInts(v, s)
	if i >= len(v) || v[i] != s {
		wh.nonempty[l] = append(v, 0)
		copy(wh.nonempty[l][i+1:], wh.nonempty[l][i:])
		wh.nonempty[l][i] = s
	}
}

// Remove unlinks a pending node from wherever it lives.
func (wh *Wheel) Remove(n *Node) {
	switch {
	case n.Level == -1:
		i := n.Pos
		last := wh.imm[len(wh.imm)-1]
		wh.imm[i], last.Pos = last, i
		wh.imm = wh.imm[:len(wh.imm)-1]
	case n.Level >= wh.lv:
		heap.Remove(wh.ovf, n.Pos)
	default:
		l := n.Level
		s := n.Abs()
		wh.rings[l].Remove(n)
		pos := s % wh.w
		_ = pos
		wh.unmarkIfEmpty(l, s)
	}
}

func (wh *Wheel) unmarkIfEmpty(l, s int) {
	if len(wh.rings[l].DrainProbe(s)) == 0 {
		wh.unmark(l, s)
	}
}

func (wh *Wheel) unmark(l, s int) {
	v := wh.nonempty[l]
	if i := sort.SearchInts(v, s); i < len(v) && v[i] == s {
		wh.nonempty[l] = append(v[:i], v[i+1:]...)
	}
}
