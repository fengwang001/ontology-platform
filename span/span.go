// Package span records original→output byte-offset correspondence as a
// sequence of boundary anchors, with binary-search bidirectional lookup.
package span

// Anchor is a shared boundary between two runs: (original offset, output offset).
type Anchor struct{ O, U int }

// Mapper is append-only while building; lookups are read-only.
type Mapper struct {
	a       []Anchor
	oCnt    int // checks spent by the most recent ToOrig
	uCnt    int // checks spent by the most recent ToOut
}

// New returns a mapper seeded with the (0,0) boundary.
func New() *Mapper { return &Mapper{a: []Anchor{{}}} }

// Copy records n bytes passed through unchanged.
func (m *Mapper) Copy(n int) {
	if n <= 0 {
		return
	}
	p := m.a[len(m.a)-1]
	m.a[len(m.a)-1] = Anchor{p.O + n, p.U + n}
}

// Delete records n original bytes removed (mapped to the run's right edge).
func (m *Mapper) Delete(n int) {
	if n <= 0 {
		return
	}
	p := m.a[len(m.a)-1]
	np := Anchor{p.O + n, p.U}
	if np != p {
		m.a = append(m.a, np)
	}
}

// Insert records n output bytes with no original counterpart.
func (m *Mapper) Insert(n int) {
	if n <= 0 {
		return
	}
	p := m.a[len(m.a)-1]
	np := Anchor{p.O, p.U + n}
	if np != p {
		m.a = append(m.a, np)
	}
}

// Rewind removes the trailing run back to the boundary with at most oLen
// original and uLen output bytes (used by the final-newline tail policy).
func (m *Mapper) Rewind(oLen, uLen int) {
	p := m.a[len(m.a)-1]
	target := Anchor{p.O - oLen, p.U - uLen}
	for len(m.a) > 1 && (m.a[len(m.a)-1].O > target.O || m.a[len(m.a)-1].U > target.U) {
		m.a = m.a[:len(m.a)-1]
	}
	m.a[len(m.a)-1] = target
}

// Anchors returns a copy of the boundary anchors.
func (m *Mapper) Anchors() []Anchor {
	out := make([]Anchor, len(m.a))
	copy(out, m.a)
	return out
}

// Replace anchors with a translated/merged global list (used by par).
func (m *Mapper) Replace(a []Anchor) { m.a = append(m.a[:0], a...) }

func clamp(x, hi int) int {
	if x < 0 {
		return 0
	}
	if x > hi {
		return hi
	}
	return x
}

// ToOrig maps an output offset to an original offset. Ties on equal
// output anchors resolve to the rightmost anchor (deleted bytes map to
// the position after themselves, i.e. before the newline).
func (m *Mapper) ToOrig(u int) int {
	u = clamp(u, m.a[len(m.a)-1].U)
	lo, hi, cnt := 0, len(m.a), 0
	for lo < hi {
		cnt++
		mid := (lo + hi) / 2
		if m.a[mid].U <= u {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	i := lo - 1
	m.oCnt = cnt
	p := m.a[i]
	if i+1 < len(m.a) {
		n := m.a[i+1]
		if n.O > p.O && n.U > p.U { // copy run
			return p.O + (u - p.U)
		}
	}
	return p.O // delete: right edge; insert run interior: fixed original point
}

// ToOut maps an original offset to an output offset. Ties resolve to the
// rightmost anchor (inserted bytes map to the position after themselves).
func (m *Mapper) ToOut(o int) int {
	o = clamp(o, m.a[len(m.a)-1].O)
	lo, hi, cnt := 0, len(m.a), 0
	for lo < hi {
		cnt++
		mid := (lo + hi) / 2
		if m.a[mid].O <= o {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	i := lo - 1
	m.uCnt = cnt
	p := m.a[i]
	if i+1 < len(m.a) {
		n := m.a[i+1]
		if n.O > p.O && n.U > p.U { // copy run
			return p.U + (o - p.O)
		}
	}
	return p.U // delete interior: right output edge; insert: fixed point
}

// Checks reports the number of intervals examined by the last lookup pair.
func (m *Mapper) Checks() int {
	c := m.oCnt
	if m.uCnt > c {
		c = m.uCnt
	}
	return c
}
