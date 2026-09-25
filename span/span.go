// Package span records correspondences between original byte ranges and
// output byte ranges for a streaming normalizer and answers bidirectional
// offset queries by binary search. It depends on no other package.
package span

// Seg is one contiguous run: original [O0,O0+Ospan) maps to output
// [U0,U0+Uspan). Exactly one of Ospan/Uspan is zero: a run is either kept
// 1:1 (Uspan==Ospan) or deleted (Uspan==0). Adjacent runs share boundaries.
type Seg struct {
	O0, Ospan int
	U0, Uspan int
}

// Map is a run-based, append-only correspondence table.
type Map struct {
	segs []Seg
	// probe counts intervals examined by the most recent ToOrig/ToOut call.
	lastProbes int
}

// Keep appends a kept run of n bytes starting at original offset o.
func (m *Map) Keep(o, n int) {
	if n <= 0 {
		return
	}
	u := 0
	if k := len(m.segs); k > 0 {
		u = m.segs[k-1].U0 + m.segs[k-1].Uspan
	}
	if k := len(m.segs); k > 0 {
		s := &m.segs[k-1]
		if s.Ospan > 0 && s.O0+s.Ospan == o && s.U0+s.Uspan == u {
			s.Ospan += n
			s.Uspan += n
			return
		}
	}
	m.segs = append(m.segs, Seg{O0: o, Ospan: n, U0: u, Uspan: n})
}

// Delete appends a deleted original run [o,o+n); it maps to output position u.
func (m *Map) Delete(o, n, u int) {
	if n <= 0 {
		return
	}
	if k := len(m.segs); k > 0 {
		s := &m.segs[k-1]
		if s.Uspan == 0 && s.O0+s.Ospan == o && s.U0 == u {
			s.Ospan += n
			return
		}
	}
	m.segs = append(m.segs, Seg{O0: o, Ospan: n, U0: u, Uspan: 0})
}

// Segs exposes the run table (read only).
func (m *Map) Segs() []Seg { return m.segs }

// Build replaces the table with segs, which must be a valid ordered table.
func (m *Map) Build(segs []Seg) { m.segs = append(m.segs[:0], segs...) }

// Synth appends one output byte that has no original byte (a synthesized
// trailing newline). anchor is the original offset both its endpoints map to
// via ToOrig; for empty input anchor 0 keeps ToOut(ToOrig(0))==0.
func (m *Map) Synth(anchor int) {
	u := 0
	if k := len(m.segs); k > 0 {
		u = m.segs[k-1].U0 + m.segs[k-1].Uspan
	}
	m.segs = append(m.segs, Seg{O0: anchor, Ospan: 0, U0: u, Uspan: 1})
}

// TrimTo drops all output after keepLen and appends one synthesized newline
// anchored at the original offset immediately following the kept content.
func (m *Map) TrimTo(keepLen, nextOrig int) {
	k := 0
	for k < len(m.segs) && m.segs[k].U0+m.segs[k].Uspan <= keepLen {
		k++
	}
	m.segs = m.segs[:k]
	if k > 0 {
		nextOrig = m.segs[k-1].O0 + m.segs[k-1].Ospan
	}
	m.segs = append(m.segs, Seg{O0: nextOrig, Ospan: 0, U0: keepLen, Uspan: 1})
}

// OrigLen is the covered original length; OutLen is the output length.
func (m *Map) OrigLen() int {
	if len(m.segs) == 0 {
		return 0
	}
	s := m.segs[len(m.segs)-1]
	return s.O0 + s.Ospan
}
func (m *Map) OutLen() int {
	if len(m.segs) == 0 {
		return 0
	}
	s := m.segs[len(m.segs)-1]
	return s.U0 + s.Uspan
}

// LastProbes returns intervals examined by the most recent directional query.
func (m *Map) LastProbes() int { return m.lastProbes }

func (m *Map) origSeg(i int) int {
	lo, hi := 0, len(m.segs)
	m.lastProbes = 0
	for lo < hi {
		mid := (lo + hi) / 2
		m.lastProbes++
		s := m.segs[mid]
		if i < s.O0 {
			hi = mid
		} else if i >= s.O0+s.Ospan {
			lo = mid + 1
		} else {
			return mid
		}
	}
	return lo
}

func (m *Map) outSeg(o int) int {
	lo, hi := 0, len(m.segs)
	m.lastProbes = 0
	for lo < hi {
		mid := (lo + hi) / 2
		m.lastProbes++
		s := m.segs[mid]
		if o < s.U0 {
			hi = mid
		} else if o >= s.U0+s.Uspan {
			lo = mid + 1
		} else {
			return mid
		}
	}
	return lo
}

// ToOrig maps an output offset o (0..OutLen) back to an original offset.
func (m *Map) ToOrig(o int) int {
	if len(m.segs) == 0 {
		return 0
	}
	k := m.outSeg(o)
	if k == len(m.segs) {
		s := m.segs[k-1]
		return s.O0 + s.Ospan
	}
	s := m.segs[k]
	if o >= s.U0+s.Uspan {
		return s.O0 + s.Ospan
	}
	return s.O0 + (o - s.U0)
}

// ToOut maps an original offset i (0..OrigLen) to an output offset. Deleted
// bytes adsorb to the run's right edge (the next kept output position).
func (m *Map) ToOut(i int) int {
	if len(m.segs) == 0 {
		return 0
	}
	k := m.origSeg(i)
	if k == len(m.segs) {
		return m.OutLen()
	}
	s := m.segs[k]
	if s.Ospan == 0 || i >= s.O0+s.Ospan {
		return s.U0 + s.Uspan
	}
	return s.U0 + (i - s.O0)
}
