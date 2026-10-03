package scrape_test

import (
	"sort"

	"ontology/head"
	"ontology/query"
	"ontology/scrape"
)

// 朴素模拟：按需求规则逐步写成, 全部线性扫描, 用于与真实实现对照。
type mSamp struct {
	ts, v int64
	stale bool
}

type mTg struct {
	last int64
	has  bool
	prev []string
}

type model struct {
	l, ooo    int64
	smax, lim int
	series    map[string][]mSamp
	tg        map[string]*mTg
}

func newModel(l, ooo int64, smax, lim int) *model {
	return &model{l: l, ooo: ooo, smax: smax, lim: lim, series: map[string][]mSamp{}, tg: map[string]*mTg{}}
}

func (m *model) append(name string, ts, v int64, stale bool) error {
	if ts < 0 || ts > 1e12 {
		return head.ErrInvalid
	}
	s := m.series[name]
	if s == nil {
		if len(m.series) >= m.smax {
			return head.ErrTooManySeries
		}
		m.series[name] = []mSamp{{ts, v, stale}}
		return nil
	}
	last := s[len(s)-1]
	if ts > last.ts {
		m.series[name] = append(s, mSamp{ts, v, stale})
		return nil
	}
	for _, c := range s {
		if c.ts == ts {
			if c.stale == stale && (stale || c.v == v) {
				return nil
			}
			return head.ErrConflict
		}
	}
	if last.ts-ts > m.ooo {
		return head.ErrTooOld
	}
	i := len(s)
	for i > 0 && s[i-1].ts > ts {
		i--
	}
	s = append(s, mSamp{})
	copy(s[i+1:], s[i:])
	s[i] = mSamp{ts, v, stale}
	m.series[name] = s
	return nil
}

type fetchSpec struct {
	kind    int // 0 正常, 1 报错, 2 panic
	payload map[string]int64
}

func (m *model) scrape(now int64, target string, spec fetchSpec) (scrape.Result, error) {
	st := m.tg[target]
	if st == nil {
		st = &mTg{}
		m.tg[target] = st
	}
	if st.has && now <= st.last {
		return scrape.Result{}, scrape.ErrClock
	}
	samples, reason := spec.payload, scrape.Ok
	switch {
	case spec.kind == 1:
		reason = scrape.Error
	case spec.kind == 2:
		reason = scrape.Panic
	case len(samples) > m.lim:
		reason = scrape.Limit
	default:
		for n := range samples {
			if n == "" || n == "up" || len(n) > 63 {
				reason = scrape.Invalid
				break
			}
		}
	}
	var names []string
	for n := range samples {
		names = append(names, n)
	}
	sort.Strings(names)
	var items []head.Item
	inRes := map[string]bool{}
	if reason == scrape.Ok {
		for _, n := range names {
			items = append(items, head.Item{Series: target + "/" + n, Ts: now, V: samples[n]})
			inRes[n] = true
		}
	}
	for _, p := range st.prev {
		if reason != scrape.Ok || !inRes[p] {
			items = append(items, head.Item{Series: target + "/" + p, Ts: now, Stale: true})
		}
	}
	up := int64(0)
	if reason == scrape.Ok {
		up = 1
	}
	items = append(items, head.Item{Series: target + "/up", Ts: now, V: up})
	backup := m.series // 预校验: 任一项失败则整体不写
	cp := map[string][]mSamp{}
	for k, v := range backup {
		cp[k] = append([]mSamp(nil), v...)
	}
	m.series = cp
	for _, x := range items {
		if err := m.append(x.Series, x.Ts, x.V, x.Stale); err != nil {
			m.series = backup
			return scrape.Result{}, err
		}
	}
	if reason == scrape.Ok {
		st.prev = names
	} else {
		st.prev = nil
	}
	st.last, st.has = now, true
	return scrape.Result{Up: reason == scrape.Ok, Reason: reason}, nil
}

func (m *model) instant(name string, t int64) query.Result {
	var latest *mSamp
	for i := range m.series[name] {
		if m.series[name][i].ts <= t {
			latest = &m.series[name][i]
		}
	}
	switch {
	case latest == nil || t-latest.ts >= m.l:
		return query.Result{Kind: query.Absent}
	case latest.stale:
		return query.Result{Kind: query.Stale}
	default:
		return query.Result{Kind: query.Value, V: latest.v}
	}
}
