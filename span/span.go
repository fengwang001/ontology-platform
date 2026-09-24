// Package span records the correspondence between original and normalized
// byte offsets as disjoint half-open segments, and answers both directions
// with binary search.
package span

// Seg is one correspondence: orig [O0,O1) <-> out [U0,U1).
// A deletion has U1==U0; an insertion has O1==O0.
type Seg struct {
	O0, O1 int
	U0, U1 int
}

// Map is a piecewise offset correspondence.
type Map struct {
	s     []Seg
	check int
}

// Add appends a segment, coalescing with the previous one when both sides are
// contiguous, so kept text occupies one segment regardless of length.
func (m *Map) Add(o0, o1, u0, u1 int) {
	if n := len(m.s); n > 0 {
		p := &m.s[n-1]
		if p.O1 == o0 && p.U1 == u0 && p.O1 > p.O0 == (o1 > o0) && p.U1 > p.U0 == (u1 > u0) {
			p.O1, p.U1 = o1, u1
			return
		}
	}
	m.s = append(m.s, Seg{o0, o1, u0, u1})
}

// Segs returns the recorded segments.
func (m *Map) Segs() []Seg { return m.s }

// Len returns original and output lengths.
func (m *Map) Len() (int, int) {
	if len(m.s) == 0 {
		return 0, 0
	}
	return m.s[len(m.s)-1].O1, m.s[len(m.s)-1].U1
}

// Cut truncates the correspondence to original length o / output length u,
// then appends an insertion to uNew when uNew > u.
func (m *Map) Cut(o, u, uNew int) {
	lo, hi := 0, len(m.s)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if m.s[mid].U1 > u {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	m.s = m.s[:lo]
	po, pu := 0, 0
	if lo > 0 {
		po, pu = m.s[lo-1].O1, m.s[lo-1].U1
	}
	if o > po || u > pu {
		m.Add(po, o, pu, u)
	}
	if uNew > u {
		m.Add(o, o, u, uNew)
	}
}

// LastCheck reports segments inspected by the most recent directional query.
func (m *Map) LastCheck() int { return m.check }

func (m *Map) find(lo, hi int, key func(int) bool) int {
	m.check = 0
	for lo < hi {
		m.check++
		mid := int(uint(lo+hi) >> 1)
		if key(mid) {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}

// ToOrig maps output offset o to an original offset. Offsets inside deletion
// gaps resolve to the following original position.
func (m *Map) ToOrig(o int) int {
	n := len(m.s)
	if n == 0 {
		return 0
	}
	k := m.find(0, n, func(i int) bool { return m.s[i].U1 > o })
	if k == n {
		return m.s[n-1].O1
	}
	s := m.s[k]
	if o <= s.U0 {
		return s.O0
	}
	return s.O0 + (o - s.U0)
}

// ToOut maps original offset o to an output offset. Offsets inside deleted
// runs resolve to the run's leading output position.
func (m *Map) ToOut(o int) int {
	n := len(m.s)
	if n == 0 {
		return 0
	}
	k := m.find(0, n, func(i int) bool { return m.s[i].O1 > o })
	if k == n {
		return m.s[n-1].U1
	}
	s := m.s[k]
	if o <= s.O0 {
		return s.U0
	}
	return s.U0 + (o - s.O0)
}
