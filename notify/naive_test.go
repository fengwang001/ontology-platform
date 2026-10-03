package notify

// 朴素模拟：用最直接的循环逐条复现规格，与 Notifier 逐步对照。
// 不共享任何状态机代码，仅复用纯函数（指纹计算）。
// 随机驱动只产生合法参数且时钟单调，故此处不重复参数/时钟校验
// （拒绝路径由 reject_test.go 确定性覆盖）。

import (
	"fmt"
	"sort"
	"strings"

	"ontology/alertstore"
	"ontology/suppress"
)

type nAlert struct {
	labels alertstore.Labels
	firing bool
	since  int64
}

type nSilence struct {
	m          suppress.Matchers
	start, end int64
}

type naive struct {
	g      []string
	w, r   int64
	amax   int
	rules  []suppress.Rule
	alerts map[string]*nAlert
	sils   map[string]*nSilence
	sent   map[string]bool
	lastAt map[string]int64
	last   map[string]map[string]bool
}

func newNaive(g []string, w, r int64, amax int, rules []suppress.Rule) *naive {
	return &naive{
		g: g, w: w, r: r, amax: amax, rules: rules,
		alerts: map[string]*nAlert{}, sils: map[string]*nSilence{},
		sent: map[string]bool{}, lastAt: map[string]int64{}, last: map[string]map[string]bool{},
	}
}

func (m *naive) fire(now int64, l alertstore.Labels) error {
	fp := alertstore.Fingerprint(l)
	if a, ok := m.alerts[fp]; ok {
		if !a.firing {
			a.firing, a.since = true, now
		}
		return nil
	}
	if len(m.alerts) >= m.amax {
		return alertstore.ErrFull
	}
	m.alerts[fp] = &nAlert{labels: l, firing: true, since: now}
	return nil
}

func (m *naive) resolve(now int64, l alertstore.Labels) error {
	a, ok := m.alerts[alertstore.Fingerprint(l)]
	if !ok || !a.firing {
		return alertstore.ErrNotFiring
	}
	a.firing = false
	return nil
}

func (m *naive) addSilence(id string, mt suppress.Matchers, s, e int64) error {
	if _, ok := m.sils[id]; ok {
		return suppress.ErrConflict
	}
	m.sils[id] = &nSilence{m: mt, start: s, end: e}
	return nil
}

func (m *naive) expireSilence(now int64, id string) error {
	s, ok := m.sils[id]
	if !ok {
		return suppress.ErrNotFound
	}
	if now < s.end {
		s.end = now
	}
	return nil
}

func (m *naive) groupKey(l alertstore.Labels) string {
	parts := make([]string, len(m.g))
	for i, g := range m.g {
		parts[i] = l[g]
	}
	return strings.Join(parts, "\x00")
}

// tick 返回通知、Sent/Failed 清单与逐组判定依据（仅用于日志）。
func (m *naive) tick(now int64, fail func(Notification) bool) (notes []Notification, sent, failed, reasons []string) {
	groups := map[string][]string{}
	for fp, a := range m.alerts {
		k := m.groupKey(a.labels)
		groups[k] = append(groups[k], fp)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fps := groups[key]
		var v, d, drop []string
		minSince := int64(-1)
		for _, fp := range fps {
			a := m.alerts[fp]
			switch {
			case a.firing && m.visible(fp, now):
				v = append(v, fp)
				if minSince < 0 || a.since < minSince {
					minSince = a.since
				}
			case !a.firing && m.sent[key] && m.last[key][fp]:
				d = append(d, fp)
			case !a.firing:
				drop = append(drop, fp)
			}
		}
		sort.Strings(v)
		sort.Strings(d)
		cond := ""
		if !m.sent[key] {
			if len(v) > 0 && now-minSince >= m.w {
				cond = "a"
			}
		} else {
			newFp := false
			for _, fp := range v {
				if !m.last[key][fp] {
					newFp = true
				}
			}
			switch {
			case newFp || len(d) > 0:
				cond = "b"
			case len(v) > 0 && now-m.lastAt[key] >= m.r:
				cond = "c"
			}
		}
		reasons = append(reasons, fmt.Sprintf("group=%q V=%v D=%v drop=%v cond=(%s)", key, v, d, drop, cond))
		if cond != "" {
			note := Notification{Group: key, Firing: orEmpty(v), Resolved: orEmpty(d), At: now}
			notes = append(notes, note)
			if fail(note) {
				failed = append(failed, key)
				continue // 整组状态保留
			}
			sent = append(sent, key)
			m.sent[key], m.lastAt[key] = true, now
			m.last[key] = map[string]bool{}
			for _, fp := range v {
				m.last[key][fp] = true
			}
			for _, fp := range d {
				delete(m.alerts, fp)
			}
		}
		for _, fp := range drop {
			delete(m.alerts, fp)
		}
		remain := false
		for _, fp := range fps {
			if _, ok := m.alerts[fp]; ok {
				remain = true
			}
		}
		if !remain {
			delete(m.sent, key)
			delete(m.lastAt, key)
			delete(m.last, key)
		}
	}
	return notes, sent, failed, reasons
}
