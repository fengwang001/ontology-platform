package nseccache_test

import (
	"fmt"
	"sort"

	"ontology/nseccache"
)

// naiveRecord 是朴素模型中的一条存活记录。
type naiveRecord struct {
	rec   nseccache.Record
	owner []string
	next  []string
	types map[uint16]bool
	exp   int
}

// naiveCache 严格按需求文字逐步实现的参考模型（线性扫描）。
type naiveCache struct {
	apex   []string
	soaMin int
	capN   int
	now    int
	hasNow bool
	recs   map[string]*naiveRecord
}

func newNaive(zone string, soaMin, capN int) *naiveCache {
	labels, err := parseLabels(zone)
	if err != nil {
		panic(err)
	}
	return &naiveCache{
		apex: labels, soaMin: soaMin, capN: capN,
		recs: make(map[string]*naiveRecord),
	}
}

func labelsKey(l []string) string {
	out := ""
	for i, x := range l {
		if i > 0 {
			out += "."
		}
		out += x
	}
	return out
}

func (m *naiveCache) sweep(now int) {
	for k, r := range m.recs {
		if now >= r.exp {
			delete(m.recs, k)
		}
	}
}

func (m *naiveCache) insert(now int, rec nseccache.Record) error {
	if now < 0 || now > 1_000_000_000_000 || rec.TTL < 0 || rec.TTL > 86400 {
		return nseccache.ErrInvalidParam
	}
	if rec.Types == nil || !rec.Types[47] {
		return nseccache.ErrInvalidParam
	}
	owner, err := parseLabels(rec.Owner)
	if err != nil {
		return nseccache.ErrInvalidParam
	}
	next, err := parseLabels(rec.Next)
	if err != nil {
		return nseccache.ErrInvalidParam
	}
	if m.hasNow && now < m.now {
		return nseccache.ErrClockRewind
	}
	if !rec.Validated {
		return nseccache.ErrNotValidated
	}
	if !inZone(owner, m.apex) || !inZone(next, m.apex) {
		return nseccache.ErrOutOfZone
	}
	m.sweep(now)
	m.now = now
	m.hasNow = true

	key := labelsKey(owner)
	delete(m.recs, key)

	eff := rec.TTL
	if m.soaMin < eff {
		eff = m.soaMin
	}
	if eff == 0 {
		return nil
	}
	if len(m.recs) >= m.capN {
		var victim *naiveRecord
		for _, r := range m.recs {
			if victim == nil || r.exp < victim.exp ||
				(r.exp == victim.exp && cmpLabels(r.owner, victim.owner) < 0) {
				victim = r
			}
		}
		delete(m.recs, labelsKey(victim.owner))
	}
	types := make(map[uint16]bool)
	for t := range rec.Types {
		types[t] = true
	}
	nr := &naiveRecord{
		rec: rec, owner: owner, next: next, types: types,
		exp: now + eff,
	}
	nr.rec.Types = types
	m.recs[key] = nr
	return nil
}

func covers(owner, next, x []string) bool {
	if cmpLabels(owner, next) < 0 {
		return cmpLabels(owner, x) < 0 && cmpLabels(x, next) < 0
	}
	return cmpLabels(x, owner) > 0 || cmpLabels(x, next) < 0
}

func (m *naiveCache) lookup(now int, qname string, qtype uint16) (nseccache.Result, error) {
	if now < 0 || now > 1_000_000_000_000 || qtype < 1 || qtype > 65535 {
		return nseccache.Result{}, nseccache.ErrInvalidParam
	}
	qn, err := parseLabels(qname)
	if err != nil {
		return nseccache.Result{}, nseccache.ErrInvalidParam
	}
	if m.hasNow && now < m.now {
		return nseccache.Result{}, nseccache.ErrClockRewind
	}
	if !inZone(qn, m.apex) {
		return nseccache.Result{}, nseccache.ErrOutOfZone
	}
	m.sweep(now)
	m.now = now
	m.hasNow = true

	if e := m.recs[labelsKey(qn)]; e != nil {
		if e.types[qtype] || (qtype != 5 && e.types[5]) {
			return nseccache.Result{Kind: nseccache.ResultMiss}, nil
		}
		return nseccache.Result{Kind: nseccache.ResultNoData, Used: []nseccache.Record{e.rec}, TTL: e.exp - now}, nil
	}

	var r *naiveRecord
	for _, e := range m.recs {
		if covers(e.owner, e.next, qn) && (r == nil || cmpLabels(e.owner, r.owner) > 0) {
			r = e
		}
	}
	if r == nil {
		return nseccache.Result{Kind: nseccache.ResultMiss}, nil
	}

	k := suffixMatch(r.owner, qn)
	if ks := suffixMatch(r.next, qn); ks > k {
		k = ks
	}
	if k == len(qn) {
		return nseccache.Result{Kind: nseccache.ResultNoData, Used: []nseccache.Record{r.rec}, TTL: r.exp - now}, nil
	}

	ce := rightSuffix(qn, k)
	wild := append([]string{"*"}, ce...)
	if _, ok := m.recs[labelsKey(wild)]; ok {
		return nseccache.Result{Kind: nseccache.ResultMiss}, nil
	}
	var wc *naiveRecord
	for _, e := range m.recs {
		if covers(e.owner, e.next, wild) && (wc == nil || cmpLabels(e.owner, wc.owner) > 0) {
			wc = e
		}
	}
	if wc == nil {
		return nseccache.Result{Kind: nseccache.ResultMiss}, nil
	}
	used := []*naiveRecord{r}
	if wc != r {
		used = append(used, wc)
	}
	sort.Slice(used, func(i, j int) bool { return cmpLabels(used[i].owner, used[j].owner) < 0 })
	recs := make([]nseccache.Record, len(used))
	ttl := -1
	for i, e := range used {
		recs[i] = e.rec
		if rem := e.exp - now; ttl < 0 || rem < ttl {
			ttl = rem
		}
	}
	return nseccache.Result{Kind: nseccache.ResultNXDomain, Used: recs, TTL: ttl}, nil
}

func parseLabels(s string) ([]string, error) {
	if len(s) == 0 || len(s) > 253 {
		return nil, fmt.Errorf("bad")
	}
	if s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	if len(s) == 0 || len(s) > 253 {
		return nil, fmt.Errorf("bad")
	}
	var labels []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i < len(s) && s[i] != '.' {
			continue
		}
		lab := s[start:i]
		if len(lab) == 0 || len(lab) > 63 {
			return nil, fmt.Errorf("bad")
		}
		b := make([]byte, len(lab))
		for j := 0; j < len(lab); j++ {
			ch := lab[j]
			switch {
			case ch >= 'A' && ch <= 'Z':
				b[j] = ch + 32
			case ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9', ch == '-' || ch == '_' || ch == '*':
				b[j] = ch
			default:
				return nil, fmt.Errorf("bad")
			}
		}
		labels = append(labels, string(b))
		start = i + 1
	}
	return labels, nil
}

func cmpLabel(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}

func cmpLabels(a, b []string) int {
	depth := len(a)
	if len(b) < depth {
		depth = len(b)
	}
	for i := 0; i < depth; i++ {
		la := a[len(a)-1-i]
		lb := b[len(b)-1-i]
		if c := cmpLabel(la, lb); c != 0 {
			return c
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}

func inZone(n, apex []string) bool {
	if len(n) < len(apex) {
		return false
	}
	for i := range apex {
		if n[len(n)-1-i] != apex[len(apex)-1-i] {
			return false
		}
	}
	return true
}

func suffixMatch(a, b []string) int {
	k := 0
	for k < len(a) && k < len(b) && a[len(a)-1-k] == b[len(b)-1-k] {
		k++
	}
	return k
}

func rightSuffix(n []string, k int) []string {
	out := make([]string, k)
	copy(out, n[len(n)-k:])
	return out
}
