// Package span 记录原文/输出区间的分段仿射映射，支持双向二分查询。
package span

// Seg：原文 [I0,I1) -> 输出 [O0,O1)。保留段等长；删除段 O1==O0；插入段 I1==I0。
type Seg struct{ I0, I1, O0, O1 int }

type Map struct {
	s      []Seg
	probes int
}

func (m *Map) last() *Seg {
	if len(m.s) == 0 {
		return nil
	}
	return &m.s[len(m.s)-1]
}
func (m *Map) OrigLen() int {
	p := m.last()
	if p == nil {
		return 0
	}
	return p.I1
}
func (m *Map) OutLen() int {
	p := m.last()
	if p == nil {
		return 0
	}
	return p.O1
}
func (m *Map) Probes() int     { return m.probes }
func (m *Map) Segments() []Seg { r := make([]Seg, len(m.s)); copy(r, m.s); return r }

func copied(a Seg) bool  { return a.O1 > a.O0 && a.I1-a.I0 == a.O1-a.O0 }
func deleted(a Seg) bool { return a.O1 == a.O0 && a.I1 > a.I0 }

func (m *Map) add(q Seg) {
	if q.I0 == q.I1 && q.O0 == q.O1 {
		return
	}
	if p := m.last(); p != nil && p.I1 == q.I0 && p.O1 == q.O0 &&
		(copied(*p) && copied(q) || deleted(*p) && deleted(q)) {
		p.I1, p.O1 = q.I1, q.O1
		return
	}
	m.s = append(m.s, q)
}

func (m *Map) Copy(n int) {
	n = max(n, 0)
	m.add(Seg{m.OrigLen(), m.OrigLen() + n, m.OutLen(), m.OutLen() + n})
}
func (m *Map) Delete(n int) {
	n = max(n, 0)
	m.add(Seg{m.OrigLen(), m.OrigLen() + n, m.OutLen(), m.OutLen()})
}
func (m *Map) Insert(b []byte) {
	n := len(b)
	m.add(Seg{m.OrigLen(), m.OrigLen(), m.OutLen(), m.OutLen() + n})
}

func (m *Map) Translate(di, do int) {
	for k := range m.s {
		q := &m.s[k]
		q.I0, q.I1, q.O0, q.O1 = q.I0+di, q.I1+di, q.O0+do, q.O1+do
	}
}
func (m *Map) Concat(parts ...*Map) {
	for _, p := range parts {
		for _, q := range p.s {
			m.add(q)
		}
	}
}

func (m *Map) DeleteOrigRange(from, to int) {
	var r []Seg
	drop := 0
	for _, p := range m.s {
		if copied(p) && p.I1 > from && p.I0 < to {
			l0, l1 := max(from, p.I0), min(to, p.I1)
			if l0 > p.I0 {
				r = append(r, Seg{p.I0, l0, p.O0, p.O0 + l0 - p.I0})
			}
			if l1 < p.I1 {
				o0 := p.O0 + l1 - p.I0 - (l1 - l0)
				r = append(r, Seg{l1, p.I1, o0, o0 + p.I1 - l1})
			}
			drop += l1 - l0
			continue
		}
		r = append(r, Seg{p.I0, p.I1, p.O0 - drop, p.O1 - drop})
	}
	m.s = r
}

func (m *Map) DeleteTailOutput(from int) {
	var r []Seg
	for _, p := range m.s {
		switch {
		case p.O1 <= from:
			r = append(r, p)
		case p.O0 < from:
			r = append(r, Seg{p.I0, p.I0 + from - p.O0, p.O0, from})
		}
	}
	m.s = r
}

func (m *Map) lowerBound(end func(Seg) int, start func(Seg) int, x int) int {
	lo, hi := 0, len(m.s)
	for lo < hi {
		m.probes++
		mid := int(uint(lo+hi) >> 1)
		q := m.s[mid]
		if end(q) < x || end(q) == x && start(q) == end(q) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

func (m *Map) ToOut(i int) int {
	m.probes = 0
	k := m.lowerBound(func(s Seg) int { return s.I1 }, func(s Seg) int { return s.I0 }, i)
	if k == len(m.s) {
		return m.OutLen()
	}
	p := m.s[k]
	if p.O1 == p.O0 {
		return p.O0
	}
	return p.O0 + max(0, i-p.I0)
}

func (m *Map) ToOrig(o int) int {
	m.probes = 0
	k := m.lowerBound(func(s Seg) int { return s.O1 }, func(s Seg) int { return s.O0 }, o)
	if k == len(m.s) {
		return m.OrigLen()
	}
	p := m.s[k]
	if p.I1 == p.I0 {
		return p.I0
	}
	return p.I0 + max(0, o-p.O0)
}
