package rollup

// 朴素模拟器：按规格独立重写落层/折叠/删除/查询；保全复用 hold.Registry。

import (
	"ontology/hold"
	"ontology/tier"
)

type simB struct{ c, s, mn, mx int64 }

func (b simB) add(v int64) simB {
	b.c++
	b.s += v
	if b.c == 1 {
		b.mn, b.mx = v, v
	} else {
		b.mn = min64(b.mn, v)
		b.mx = max64(b.mx, v)
	}
	return b
}
func (b simB) merge(x simB) simB {
	if x.c == 0 {
		return b
	}
	if b.c == 0 {
		return x
	}
	b.c += x.c
	b.s += x.s
	b.mn = min64(b.mn, x.mn)
	b.mx = max64(b.mx, x.mx)
	return b
}
func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

type naive struct {
	now, a0, a1, a2, cap int64
	l0                   []tier.Point
	l1, l2               map[int64]simB
	reg                  *hold.Registry
}

func newNaive(a0, a1, a2, cap int64, reg *hold.Registry) *naive {
	return &naive{a0: a0, a1: a1, a2: a2, cap: cap, reg: reg,
		l1: map[int64]simB{}, l2: map[int64]simB{}}
}
func (n *naive) units() int64 { return int64(len(n.l0) + len(n.l1) + len(n.l2)) }

func (n *naive) write(ts, v int64) error {
	if ts < 0 || ts > 1e13 || v < -1e12 || v > 1e12 {
		return tier.ErrInvalidArgument
	}
	mi, ho := ts/60000, ts/3600000
	mf, mt := mi*60000, (mi+1)*60000
	put := func(bs map[int64]simB, k int64) error {
		b, ex := bs[k]
		if !ex && n.units() >= n.cap {
			return tier.ErrCapacity
		}
		bs[k] = b.add(v)
		return nil
	}
	if !n.reg.IntersectsInterval(mf, mt) {
		he := (ho + 1) * 3600000
		switch {
		case n.now-he >= n.a2:
			return tier.ErrExpired
		case n.now-he >= n.a1:
			return put(n.l2, ho)
		case n.now-mt >= n.a0:
			return put(n.l1, mi)
		}
	}
	if n.units() >= n.cap {
		return tier.ErrCapacity
	}
	n.l0 = append(n.l0, tier.Point{TS: ts, V: v})
	return nil
}

// advance：删除过期小时、L0→L1、L1→L2 三阶段。
func (n *naive) advance(now int64) error {
	if now < 0 || now > 1e13 {
		return tier.ErrInvalidArgument
	}
	if now < n.now {
		return tier.ErrClockRollback
	}
	if now == n.now {
		return nil
	}
	held := func(f, t int64) bool { return n.reg.IntersectsInterval(f, t) }
	hours := map[int64]bool{}
	for _, p := range n.l0 {
		hours[p.TS/3600000] = true
	}
	for k := range n.l1 {
		hours[k/60] = true
	}
	for k := range n.l2 {
		hours[k] = true
	}
	for h := range hours {
		hf, ht := h*3600000, (h+1)*3600000
		if now-ht < n.a2 || held(hf, ht) {
			continue
		}
		kept := n.l0[:0]
		for _, p := range n.l0 {
			if p.TS < hf || p.TS >= ht {
				kept = append(kept, p)
			}
		}
		n.l0 = kept
		for k := range n.l1 {
			if kf := k * 60000; hf <= kf && kf < ht {
				delete(n.l1, k)
			}
		}
		delete(n.l2, h)
	}
	g, kept := map[int64][]int64{}, n.l0[:0]
	for _, p := range n.l0 {
		mi := p.TS / 60000
		mf, mt := mi*60000, (mi+1)*60000
		if now-mt >= n.a0 && !held(mf, mt) {
			g[mi] = append(g[mi], p.V)
		} else {
			kept = append(kept, p)
		}
	}
	n.l0 = kept
	for mi, vs := range g {
		b := n.l1[mi]
		for _, v := range vs {
			b = b.add(v)
		}
		n.l1[mi] = b
	}
	for k := range n.l1 {
		h := k / 60
		hf, ht := h*3600000, (h+1)*3600000
		if now-ht >= n.a1 && !held(hf, ht) {
			n.l2[h] = n.l2[h].merge(n.l1[k])
			delete(n.l1, k)
		}
	}
	n.now = now
	return nil
}

func (n *naive) query(f, t int64) QueryResult {
	r := QueryResult{}
	add := func(c, ss, mn, mx int64) {
		if r.Count == 0 {
			r.Min, r.Max = mn, mx
		} else {
			r.Min = min64(r.Min, mn)
			r.Max = max64(r.Max, mx)
		}
		r.Count, r.Sum = r.Count+c, r.Sum+ss
	}
	scan := func(bs map[int64]simB, w int64) {
		for k, b := range bs {
			bf, bt := k*w, (k+1)*w
			if bf >= f && bt <= t {
				add(b.c, b.s, b.mn, b.mx)
			} else if bf < t && f < bt {
				r.Skipped += b.c
			}
		}
	}
	for _, p := range n.l0 {
		if f <= p.TS && p.TS < t {
			add(1, p.V, p.V, p.V)
		}
	}
	scan(n.l1, 60000)
	scan(n.l2, 3600000)
	return r
}
