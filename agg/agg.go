package agg

import (
	"container/heap"
	"errors"
	"math"

	"ontology/delta"
)

var (
	ErrRetractMissing = errors.New("agg: retraction of a value that is not alive")
	ErrSumOverflow    = errors.New("agg: SUM would overflow int64")
)

// bh: min-heap of distinct live values (max-heap when rev). pos gives O(log m)
// removal; each Swap visits one stored value and bumps the Group's accessed.
type bh struct {
	v   []int64
	pos map[int64]int
	rev bool
	ac  *int
}

func (h bh) Len() int           { return len(h.v) }
func (h bh) Less(i, j int) bool { return (h.v[i] < h.v[j]) == !h.rev }
func (h *bh) Swap(i, j int) {
	h.v[i], h.v[j] = h.v[j], h.v[i]
	h.pos[h.v[i]], h.pos[h.v[j]] = i, j
	*h.ac++
}
func (h *bh) Push(x any) { v := x.(int64); h.pos[v] = len(h.v); h.v = append(h.v, v) }
func (h *bh) Pop() any {
	v := h.v[len(h.v)-1]
	delete(h.pos, v)
	h.v = h.v[:len(h.v)-1]
	return v
}

type Group struct {
	n, s     int64
	freq     map[int64]int
	lo, hi   bh
	accessed int
}

func (g *Group) init() {
	g.freq = map[int64]int{}
	g.lo = bh{pos: map[int64]int{}, ac: &g.accessed}
	g.hi = bh{pos: map[int64]int{}, ac: &g.accessed, rev: true}
}
func (g *Group) add(v int64) {
	if g.freq == nil {
		g.init()
	}
	g.n++
	g.s += v
	if g.freq[v]++; g.freq[v] == 1 {
		heap.Push(&g.lo, v)
		heap.Push(&g.hi, v)
	}
}
func (g *Group) drop(v int64) {
	g.n--
	g.s -= v
	if g.freq[v]--; g.freq[v] == 0 {
		heap.Remove(&g.lo, g.lo.pos[v])
		heap.Remove(&g.hi, g.hi.pos[v])
		delete(g.freq, v)
	}
}
func overflow(s, v int64) bool {
	return v > 0 && s > math.MaxInt64-v || v < 0 && s < math.MinInt64-v
}

// Apply validates before any mutation: bad op, retraction of a non-live value,
// and SUM overflow are all rejected with no field changed.
func (g *Group) Apply(ev delta.Event) error {
	if err := ev.Validate(); err != nil {
		return err
	}
	g.accessed = 0
	if ev.Op == delta.Retract && g.freq[ev.Val] == 0 {
		return ErrRetractMissing
	}
	if ev.Op == delta.Insert && overflow(g.s, ev.Val) {
		return ErrSumOverflow
	}
	if ev.Op == delta.Insert {
		g.add(ev.Val)
	} else {
		g.drop(ev.Val)
	}
	return nil
}

// Value signals absence via hasMin/hasMax: an empty group is not a zero value.
func (g *Group) Value() (n, s, lo, hi int64, hasMin, hasMax bool) {
	if g.n > 0 {
		return g.n, g.s, g.lo.v[0], g.hi.v[0], true, true
	}
	return 0, 0, 0, 0, false, false
}
func (g *Group) Empty() bool { return g.n == 0 }

func AccessBoundOK() bool {
	for _, m := range []int{100, 1000, 10000} {
		var g Group
		for i := range m {
			_ = g.Apply(delta.Event{Val: int64(i), Op: delta.Insert})
		}
		_ = g.Apply(delta.Event{Val: -1, Op: delta.Insert})
		if float64(g.accessed) > 6*math.Log2(float64(m+1)) {
			return false
		}
	}
	return true
}
