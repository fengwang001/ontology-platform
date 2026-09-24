// Package agg incrementally maintains COUNT/SUM/MIN/MAX per group.
package agg

import "errors"
import "math"
import "ontology/delta"

var ErrRetractUnknown, ErrSumOverflow, errSlow = errors.New("agg: retraction of a value never inserted"), errors.New("agg: sum overflow"), errors.New("agg: self-check detected an O(m) scan")

// node is one ordered-multiset treap node; priorities are hash64(key), expected depth O(log m).
type node struct {
	key  int64
	cnt  int
	prio uint64
	l, r *node
}

// Group is one group's live multiset; the unexported touched counts values visited by the latest Apply.
type Group struct {
	n       int
	sum     int64
	root    *node
	touched int
}

func New() *Group { return &Group{} }

// SelfCheck builds groups of several sizes and verifies one Apply visits at most C*log2(m)+K values; it exposes no counter.
func SelfCheck() error {
	for _, m := range []int{100, 1000, 10000} {
		g := New()
		for i := 0; i < m; i++ {
			g.Apply(delta.Event{Val: int64(i) * 7919, Op: delta.Insert})
		}
		g.Apply(delta.Event{Val: int64(m)*7919 + 1, Op: delta.Insert})
		if float64(g.touched) > 4*math.Log2(float64(m))+8 {
			return errSlow
		}
	}
	return nil
}

// Apply inserts/retracts one value; all checks precede all mutations, so a rejected Apply changes nothing.
func (g *Group) Apply(ev delta.Event) error {
	g.touched = 0
	if ev.Op == delta.Insert {
		if ev.Val > 0 && g.sum > math.MaxInt64-ev.Val || ev.Val < 0 && g.sum < math.MinInt64-ev.Val {
			return ErrSumOverflow
		}
		g.root = msIns(g.root, ev.Val, &g.touched)
		g.n++
		g.sum += ev.Val
		return nil
	}
	nr, found := msDel(g.root, ev.Val, &g.touched)
	if !found {
		return ErrRetractUnknown
	}
	g.root = nr
	g.n--
	g.sum -= ev.Val // back to a previously valid sum; cannot overflow
	return nil
}
func (g *Group) Count() int         { return g.n }
func (g *Group) Sum() int64         { return g.sum }
func (g *Group) Min() (int64, bool) { return g.edge(false) }
func (g *Group) Max() (int64, bool) { return g.edge(true) }
func (g *Group) edge(right bool) (int64, bool) {
	for t := g.root; t != nil; {
		n := t.l
		if right {
			n = t.r
		}
		if n == nil {
			return t.key, true
		}
		t = n
	}
	return 0, false // empty: "absent", never the impostor zero
}
func hash64(x int64) uint64 { // splitmix64 finalizer: iid-like priorities for any key pattern
	z := uint64(x) + 0x9e3779b97f4a7c15
	z = (z ^ z>>30) * 0xbf58476d1ce4e5b9
	z = (z ^ z>>27) * 0x94d049bb133111eb
	return z ^ z>>31
}
func rotR(c *node) *node { a := c.l; c.l = a.r; a.r = c; return a }
func rotL(c *node) *node { a := c.r; c.r = a.l; a.l = c; return a }

// msIns inserts one occurrence, rotating to keep treap order; called only after pre-checks pass.
func msIns(t *node, key int64, k *int) *node {
	if t == nil {
		return &node{key: key, cnt: 1, prio: hash64(key)}
	}
	*k++
	if key == t.key {
		t.cnt++
		return t
	}
	if key < t.key {
		t.l = msIns(t.l, key, k)
		if t.l.prio > t.prio {
			return rotR(t)
		}
	} else {
		t.r = msIns(t.r, key, k)
		if t.r.prio > t.prio {
			return rotL(t)
		}
	}
	return t
}

// msDel removes one occurrence; absent key returns the same pointers with found=false.
func msDel(t *node, key int64, k *int) (*node, bool) {
	if t == nil {
		return nil, false
	}
	*k++
	if key == t.key {
		t.cnt--
		if t.cnt > 0 {
			return t, true
		}
		return msMerge(t.l, t.r, k), true
	}
	if key < t.key {
		nl, ok := msDel(t.l, key, k)
		t.l = nl // nl == old child when !ok: no observable mutation
		return t, ok
	}
	nr, ok := msDel(t.r, key, k)
	t.r = nr
	return t, ok
}
func msMerge(a, b *node, k *int) *node { // all keys of a precede all keys of b
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	*k++
	if a.prio > b.prio {
		a.r = msMerge(a.r, b, k)
		return a
	}
	b.l = msMerge(a, b.l, k)
	return b
}
