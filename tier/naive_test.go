package tier

// 与实现独立的朴素直接状态机：显式三层，Advance 字面执行删除、L0→L1、L1→L2。
// 保全只影响当前动作，不恢复已折叠数据（故不能从原始点重算）。

import (
	"sort"

	"ontology/rollup"
)

type nState struct {
	now, a0, a1, a2, cap int64
	l0                   []rollup.Point
	l1, l2               map[int64]rollup.Bucket
	holds                []nHold
}
type nHold struct {
	id       string
	from, to int64
}

func newNaive(a0, a1, a2, cap int64) *nState {
	return &nState{a0: a0, a1: a1, a2: a2, cap: cap,
		l1: map[int64]rollup.Bucket{}, l2: map[int64]rollup.Bucket{}}
}

func (n *nState) hit(from, to int64) bool {
	for _, h := range n.holds {
		if h.from < to && from < h.to {
			return true
		}
	}
	return false
}

func (n *nState) units() int { return len(n.l0) + len(n.l1) + len(n.l2) }

func (n *nState) addL0(p rollup.Point) {
	i := sort.Search(len(n.l0), func(k int) bool { return n.l0[k].TS > p.TS })
	n.l0 = append(n.l0, rollup.Point{})
	copy(n.l0[i+1:], n.l0[i:])
	n.l0[i] = p
}

func (n *nState) write(ts, v int64) error {
	m, h := rollup.MinuteOf(ts), rollup.HourOf(ts)
	mf, mt := rollup.MinuteRange(m)
	_, ht := rollup.HourRange(h)
	held := n.hit(mf, mt)
	if !held && n.now-ht >= n.a2 {
		return ErrExpired
	}
	exists := false
	switch {
	case !held && n.now-ht >= n.a1:
		_, exists = n.l2[h]
	case !held && n.now-mt >= n.a0:
		_, exists = n.l1[m]
	}
	if !exists && n.units()+1 > int(n.cap) {
		return ErrCapacity
	}
	switch {
	case !held && n.now-ht >= n.a1:
		n.l2[h] = mustAdd(n.l2[h], v)
	case !held && n.now-mt >= n.a0:
		n.l1[m] = mustAdd(n.l1[m], v)
	default:
		n.addL0(rollup.Point{TS: ts, V: v})
	}
	return nil
}

func (n *nState) advance(now int64) {
	n.now = now
	// ① 删除够龄且不与保全相交的小时在三层的全部数据。
	hours := map[int64]struct{}{}
	for _, p := range n.l0 {
		hours[rollup.HourOf(p.TS)] = struct{}{}
	}
	for m := range n.l1 {
		hours[m/60] = struct{}{}
	}
	for h := range n.l2 {
		hours[h] = struct{}{}
	}
	for h := range hours {
		from, to := rollup.HourRange(h)
		if now-to < n.a2 || n.hit(from, to) {
			continue
		}
		delete(n.l2, h)
		for m := h * 60; m < (h+1)*60; m++ {
			delete(n.l1, m)
		}
		kept := n.l0[:0]
		for _, p := range n.l0 {
			if rollup.HourOf(p.TS) != h {
				kept = append(kept, p)
			}
		}
		n.l0 = append([]rollup.Point(nil), kept...)
	}
	// ② L0→L1。
	groups := map[int64]rollup.Bucket{}
	kept := []rollup.Point{}
	for _, p := range n.l0 {
		m := rollup.MinuteOf(p.TS)
		from, to := rollup.MinuteRange(m)
		if now-to >= n.a0 && !n.hit(from, to) {
			groups[m] = mustAdd(groups[m], p.V)
		} else {
			kept = append(kept, p)
		}
	}
	n.l0 = kept
	keys := []int64{}
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, k := range keys {
		n.l1[k] = mustMerge(n.l1[k], groups[k])
	}
	// ③ L1→L2。
	byH := map[int64]rollup.Bucket{}
	mins := map[int64][]int64{}
	for m, b := range n.l1 {
		h := m / 60
		byH[h] = mustMerge(byH[h], b)
		mins[h] = append(mins[h], m)
	}
	keys = keys[:0]
	for h := range byH {
		keys = append(keys, h)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, h := range keys {
		from, to := rollup.HourRange(h)
		if now-to >= n.a1 && !n.hit(from, to) {
			n.l2[h] = mustMerge(n.l2[h], byH[h])
			for _, m := range mins[h] {
				delete(n.l1, m)
			}
		}
	}
}

func (n *nState) query(from, to int64) Result {
	var r Result
	acc := func(b rollup.Bucket) {
		m := mustMerge(rollup.Bucket{Count: r.Count, Sum: r.Sum, Min: r.Min, Max: r.Max}, b)
		r.Count, r.Sum, r.Min, r.Max = m.Count, m.Sum, m.Min, m.Max
	}
	skip := func(bs, be int64, b rollup.Bucket) {
		if bs >= from && be <= to {
			acc(b)
		} else if bs < to && be > from {
			r.Skipped += b.Count
		}
	}
	for _, p := range n.l0 {
		if p.TS >= from && p.TS < to {
			acc(rollup.Bucket{Count: 1, Sum: p.V, Min: p.V, Max: p.V})
		}
	}
	for k, b := range n.l1 {
		f, t := rollup.MinuteRange(k)
		skip(f, t, b)
	}
	for k, b := range n.l2 {
		f, t := rollup.HourRange(k)
		skip(f, t, b)
	}
	if r.Count == 0 {
		r.Min, r.Max = 0, 0
	}
	return r
}

func mustAdd(b rollup.Bucket, v int64) rollup.Bucket {
	nb, err := rollup.AddPoint(b, v)
	if err != nil {
		panic("naive overflow")
	}
	return nb
}

func mustMerge(a, b rollup.Bucket) rollup.Bucket {
	m, err := rollup.Merge(a, b)
	if err != nil {
		panic("naive overflow")
	}
	return m
}

func dumpNaive(n *nState) string {
	return dump(&Store{l0: n.l0, l1: n.l1, l2: n.l2})
}
