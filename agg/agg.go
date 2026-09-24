package agg

import "errors"
import "ontology/delta"

var ErrRetractMissing, ErrOverflow = errors.New("agg: retract of a value that is not live"), errors.New("agg: SUM overflows int64")

type Counter struct {
	Count, Sum, Min, Max int64
	HasMin, HasMax       bool
}
type node struct {
	key int64
	cnt int
	pr  uint64
	ch  [2]*node // 0=left (smaller), 1=right
}

// merge joins two treaps with all keys of a below all keys of b.
func merge(a, b *node, p *int) *node {
	if a == nil || b == nil {
		if a == nil {
			return b
		}
		return a
	}
	*p++
	if a.pr > b.pr {
		a.ch[1] = merge(a.ch[1], b, p)
		return a
	}
	b.ch[0] = merge(a, b.ch[0], p)
	return b
}

// edit applies one copy delta d (+1/-1); p counts nodes visited. ok=false
// means a retract found no live value and nothing changed.
func edit(n *node, v int64, d int, p *int) (*node, bool) {
	if n == nil {
		if d < 0 {
			return nil, false
		}
		return &node{key: v, cnt: 1, pr: uint64(v)*0x9e3779b97f4a7c15 ^ uint64(v)>>17}, true
	}
	*p++
	if v == n.key {
		if n.cnt += d; n.cnt > 0 {
			return n, true
		}
		return merge(n.ch[0], n.ch[1], p), true
	}
	i := 1
	if v < n.key {
		i = 0
	}
	var ok bool
	if n.ch[i], ok = edit(n.ch[i], v, d, p); !ok {
		return n, false
	}
	if x := n.ch[i]; d > 0 && x != nil && x.pr > n.pr { // zig the heavy child up
		n.ch[i] = x.ch[1-i]
		x.ch[1-i] = n
		n = x
	}
	return n, true
}

type Group struct {
	n, sum int64
	root   *node
	probes int
}

// Apply validates fully before mutating, so a rejection leaves no trace.
func (g *Group) Apply(ev delta.Event) error {
	g.probes = 0
	d := int64(1)
	if ev.Op == delta.Retract {
		d = -1
	} else if ev.Op != delta.Insert {
		return delta.ErrInvalidEvent
	}
	s := g.sum + d*ev.Val
	if d > 0 && ((ev.Val > 0 && s < g.sum) || (ev.Val < 0 && s > g.sum)) {
		return ErrOverflow
	}
	r, ok := edit(g.root, ev.Val, int(d), &g.probes)
	if !ok {
		return ErrRetractMissing
	}
	g.root, g.sum, g.n = r, s, g.n+d
	return nil
}

// Value is a pure read; an empty group reports HasMin/HasMax=false, never 0.
func (g *Group) Value() Counter {
	c := Counter{Count: g.n, Sum: g.sum}
	if g.root == nil {
		return c
	}
	lo, hi := g.root, g.root
	for lo.ch[0] != nil {
		lo = lo.ch[0]
	}
	for hi.ch[1] != nil {
		hi = hi.ch[1]
	}
	c.Min, c.Max, c.HasMin, c.HasMax = lo.key, hi.key, true, true
	return c
}
