package limiter

import (
	"errors"
	"sort"
	"strings"

	"ontology/rule"
)

// naiveModel 是按题面逐步手写的独立参考实现，刻意不依赖被测包内部。

type nCounter struct {
	k, cur, prev int64
}

type nRule struct {
	id      string
	pattern []rule.Pair
	l, w    int64
	mode    rule.Mode
}

type naiveModel struct {
	rules  map[string]*nRule
	counts map[string]*nCounter
	shadow map[string]int64
	maxNow int64
}

func newNaive() *naiveModel {
	return &naiveModel{
		rules:  map[string]*nRule{},
		counts: map[string]*nCounter{},
		shadow: map[string]int64{},
	}
}

func nValidRule(id string, pattern []rule.Pair, l, w int64, mode rule.Mode) bool {
	if id == "" || len(pattern) < 1 || len(pattern) > 3 {
		return false
	}
	seen := map[string]bool{}
	for _, p := range pattern {
		if p.Key == "" || p.Value == "" || seen[p.Key] {
			return false
		}
		seen[p.Key] = true
	}
	return l >= 1 && l <= 1e9 && w >= 1 && w <= 1e9 &&
		(mode == rule.Enforce || mode == rule.Shadow)
}

func nSig(pattern []rule.Pair) string {
	ps := append([]rule.Pair(nil), pattern...)
	sort.Slice(ps, func(i, j int) bool { return ps[i].Key < ps[j].Key })
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = p.Key + "=" + p.Value
	}
	return strings.Join(parts, "\x00")
}

func (m *naiveModel) add(id string, pattern []rule.Pair, l, w int64, mode rule.Mode) error {
	if !nValidRule(id, pattern, l, w, mode) {
		return errors.New("invalid")
	}
	sig := nSig(pattern)
	if _, ok := m.rules[id]; ok {
		return errors.New("exists")
	}
	for _, r := range m.rules {
		if nSig(r.pattern) == sig {
			return errors.New("duplicate")
		}
	}
	m.rules[id] = &nRule{id: id, pattern: append([]rule.Pair(nil), pattern...), l: l, w: w, mode: mode}
	return nil
}

func (m *naiveModel) setMode(id string, mode rule.Mode) error {
	if mode != rule.Enforce && mode != rule.Shadow {
		return errors.New("invalid")
	}
	r, ok := m.rules[id]
	if !ok {
		return errors.New("notfound")
	}
	r.mode = mode
	return nil
}

func (m *naiveModel) remove(id string) error {
	if _, ok := m.rules[id]; !ok {
		return errors.New("notfound")
	}
	delete(m.rules, id)
	delete(m.shadow, id)
	prefix := id + "\x00"
	for s := range m.counts {
		if strings.HasPrefix(s, prefix) {
			delete(m.counts, s)
		}
	}
	return nil
}

type nDecision struct {
	allowed      bool
	rejectedBy   string
	selected     []string
	shadowReject []string
}

func nMatch(pattern []rule.Pair, desc rule.Descriptor) bool {
	for _, p := range pattern {
		v, ok := desc[p.Key]
		if !ok {
			return false
		}
		if p.Value != "*" && p.Value != v {
			return false
		}
	}
	return true
}

func nRoll(c *nCounter, kp int64) {
	switch {
	case kp == c.k:
	case kp == c.k+1:
		c.prev = c.cur
		c.cur = 0
		c.k = kp
	default:
		c.prev = 0
		c.cur = 0
		c.k = kp
	}
}

func nEst(c *nCounter, now, w int64) int64 {
	return c.cur + c.prev*(w-now%w)/w
}

func (m *naiveModel) allow(desc rule.Descriptor, now int64) (nDecision, error) {
	if len(desc) < 1 || len(desc) > 8 {
		return nDecision{}, errors.New("invalid")
	}
	for k, v := range desc {
		if k == "" || v == "" {
			return nDecision{}, errors.New("invalid")
		}
	}
	if now < 0 || now > 1e15 {
		return nDecision{}, errors.New("time")
	}
	if now < m.maxNow {
		return nDecision{}, errors.New("back")
	}
	m.maxNow = now

	type cand struct {
		id    string
		r     *nRule
		exact int
	}
	groups := map[string]cand{}
	for _, r := range m.rules {
		if !nMatch(r.pattern, desc) {
			continue
		}
		keys := make([]string, len(r.pattern))
		exact := 0
		for i, p := range r.pattern {
			keys[i] = p.Key
			if p.Value != "*" {
				exact++
			}
		}
		sort.Strings(keys)
		g := strings.Join(keys, "\x00")
		best, ok := groups[g]
		if !ok || exact > best.exact || (exact == best.exact && r.id < best.id) {
			groups[g] = cand{id: r.id, r: r, exact: exact}
		}
	}
	chosen := make([]cand, 0, len(groups))
	for _, c := range groups {
		chosen = append(chosen, c)
	}
	sort.Slice(chosen, func(i, j int) bool { return chosen[i].id < chosen[j].id })

	d := nDecision{allowed: true, selected: []string{}, shadowReject: []string{}}
	for _, c := range chosen {
		d.selected = append(d.selected, c.id)
	}

	type item struct {
		id  string
		r   *nRule
		key string
		est int64
	}
	items := make([]item, 0, len(chosen))
	for _, c := range chosen {
		vals := make([]string, len(c.r.pattern))
		for i, p := range c.r.pattern {
			vals[i] = desc[p.Key]
		}
		key := c.id + "\x00" + strings.Join(vals, "\x00")
		est := int64(0)
		if cnt, ok := m.counts[key]; ok {
			tmp := *cnt
			nRoll(&tmp, now/c.r.w)
			est = nEst(&tmp, now, c.r.w)
		}
		items = append(items, item{id: c.id, r: c.r, key: key, est: est})
	}

	reject := ""
	for _, it := range items {
		if it.r.mode == rule.Enforce && it.est+1 > it.r.l {
			if reject == "" || it.id < reject {
				reject = it.id
			}
		}
	}
	if reject != "" {
		d.allowed = false
		d.rejectedBy = reject
		return d, nil
	}
	for _, it := range items {
		cnt, ok := m.counts[it.key]
		if !ok {
			cnt = &nCounter{k: now / it.r.w}
			m.counts[it.key] = cnt
		}
		nRoll(cnt, now/it.r.w)
		if cnt.cur < it.r.l+1 {
			cnt.cur++
		}
		if it.r.mode == rule.Shadow && it.est+1 > it.r.l {
			m.shadow[it.id]++
			d.shadowReject = append(d.shadowReject, it.id)
		}
	}
	sort.Strings(d.shadowReject)
	return d, nil
}

func (m *naiveModel) state() (map[string][3]int64, map[string]int64, int) {
	cs := map[string][3]int64{}
	for k, c := range m.counts {
		cs[k] = [3]int64{c.k, c.cur, c.prev}
	}
	sh := map[string]int64{}
	for k, v := range m.shadow {
		sh[k] = v
	}
	return cs, sh, len(m.counts)
}
