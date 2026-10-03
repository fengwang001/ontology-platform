package scrape_test

import (
	"sort"

	"ontology/head"
	"ontology/query"
	"ontology/scrape"
)

// 朴素模拟：按题述规则独立实现的参照（map 存储 + 线性扫描）。
// 假设同一批次不含重复序列（Scrape 写入集与单项直写均满足）。
type naive struct {
	l, ooo int64
	smax   int
	series map[string]map[int64]head.Sample
	dups   int64
}

func newNaive(l, ooo int64, smax int) *naive {
	return &naive{l: l, ooo: ooo, smax: smax, series: map[string]map[int64]head.Sample{}}
}

func (n *naive) check(name string, smp head.Sample, extraNew int) (bool, error) {
	if name == "" || len(name) > 128 || smp.Ts < 0 || smp.Ts > 1e12 {
		return false, head.ErrInvalidParam
	}
	m, ok := n.series[name]
	if !ok && len(n.series)+extraNew >= n.smax {
		return false, head.ErrTooManySeries
	}
	if old, ok := m[smp.Ts]; ok {
		if old.Stale == smp.Stale && (smp.Stale || old.V == smp.V) {
			return true, nil
		}
		return false, head.ErrConflict
	}
	maxTs, has := int64(0), false
	for ts := range m {
		if !has || ts > maxTs {
			maxTs, has = ts, true
		}
	}
	if has && smp.Ts < maxTs && maxTs-smp.Ts > n.ooo {
		return false, head.ErrTooOld
	}
	return false, nil
}

func (n *naive) batch(items []head.Item) error {
	added := map[string]bool{}
	var dups int64
	for _, it := range items {
		_, committed := n.series[it.Series]
		extra := len(added)
		if !committed {
			added[it.Series] = true
		}
		dup, err := n.check(it.Series, head.Sample{Ts: it.Ts, V: it.V, Stale: it.Stale}, extra)
		if err != nil {
			return err
		}
		if dup {
			dups++
		}
	}
	for _, it := range items {
		m := n.series[it.Series]
		if m == nil {
			m = map[int64]head.Sample{}
			n.series[it.Series] = m
		}
		if _, ok := m[it.Ts]; !ok {
			m[it.Ts] = head.Sample{Ts: it.Ts, V: it.V, Stale: it.Stale}
		}
	}
	n.dups += dups
	return nil
}

func (n *naive) instant(name string, t int64) query.Result {
	best, found := head.Sample{}, false
	for ts, smp := range n.series[name] {
		if ts <= t && (!found || ts > best.Ts) {
			best, found = smp, true
		}
	}
	if !found || t-best.Ts >= n.l {
		return query.Result{Kind: query.Absent}
	}
	if best.Stale {
		return query.Result{Kind: query.Stale}
	}
	return query.Result{Kind: query.Value, V: best.V}
}

func (n *naive) snapshot() map[string][]head.Sample {
	out := make(map[string][]head.Sample, len(n.series))
	for name, m := range n.series {
		for _, smp := range m {
			out[name] = append(out[name], smp)
		}
		sort.Slice(out[name], func(i, j int) bool { return out[name][i].Ts < out[name][j].Ts })
	}
	return out
}

type naiveTarget struct {
	name string
	last int64
	has  bool
	prev map[string]bool
}

// naiveScrape 模拟一次抓取：outcome 0=成功 1=error 2=panic。
func naiveScrape(n *naive, lim int, tg *naiveTarget, now int64, outcome int, values map[string]int64) (scrape.Result, error) {
	if tg.has && now <= tg.last {
		return scrape.Result{}, scrape.ErrClockRegression
	}
	reason := scrape.Ok
	switch {
	case outcome == 2:
		reason = scrape.Panic
	case outcome == 1:
		reason = scrape.Error
	case len(values) > lim:
		reason = scrape.Limit
	default:
		for name := range values {
			if name == "" || name == "up" || len(name) > 63 {
				reason = scrape.Invalid
			}
		}
	}
	items, up, prev := naiveItems(tg, now, values, reason)
	if err := n.batch(items); err != nil {
		return scrape.Result{}, err
	}
	tg.last, tg.has, tg.prev = now, true, prev
	return scrape.Result{Up: up, Reason: reason}, nil
}

func naiveItems(tg *naiveTarget, now int64, m map[string]int64, r scrape.Reason) ([]head.Item, int64, map[string]bool) {
	key := func(n string) string { return tg.name + "/" + n }
	next := map[string]bool{}
	var items []head.Item
	if r != scrape.Ok {
		m = nil // 失败时 fetch 结果丢弃，标记不带值
	}
	add := func(names []string, stale bool) {
		sort.Strings(names)
		for _, n := range names {
			items = append(items, head.Item{Series: key(n), Ts: now, V: m[n], Stale: stale})
		}
	}
	if r == scrape.Ok {
		var names, gone []string
		for n := range m {
			names, next[n] = append(names, n), true
		}
		for n := range tg.prev {
			if _, ok := m[n]; !ok {
				gone = append(gone, n)
			}
		}
		add(names, false)
		add(gone, true)
		return append(items, head.Item{Series: key("up"), Ts: now, V: 1}), 1, next
	}
	var names []string
	for n := range tg.prev {
		names = append(names, n)
	}
	add(names, true)
	return append(items, head.Item{Series: key("up"), Ts: now}), 0, next
}
