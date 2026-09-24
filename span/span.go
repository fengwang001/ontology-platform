// Package span records the correspondence between original byte intervals
// and normalized output byte intervals and answers both directions via
// binary search. Adjacent same-shape segments are coalesced, so the number
// of segments grows only with the number of deletion points.
package span

type seg struct {
	orig, out, olen, ulen int
}

// Mapper is an append-only interval map. The zero value is ready.
// It is not safe for concurrent use.
type Mapper struct {
	segs       []seg
	lastChecks int
}

// Copy appends a 1:1 copied region of n bytes starting at the current ends.
func (m *Mapper) Copy(n int) {
	if n <= 0 {
		return
	}
	if k := len(m.segs); k > 0 {
		s := &m.segs[k-1]
		if s.olen == s.ulen {
			s.olen += n
			s.ulen += n
			return
		}
	}
	m.segs = append(m.segs, seg{orig: m.OrigLen(), out: m.OutLen(), olen: n, ulen: n})
}

// Delete appends a deleted original region of n bytes (no output).
func (m *Mapper) Delete(n int) {
	if n <= 0 {
		return
	}
	if k := len(m.segs); k > 0 {
		s := &m.segs[k-1]
		if s.ulen == 0 {
			s.olen += n
			return
		}
	}
	m.segs = append(m.segs, seg{orig: m.OrigLen(), out: m.OutLen(), olen: n})
}

// Insert appends an inserted output region of n bytes (no original bytes).
func (m *Mapper) Insert(n int) {
	if n <= 0 {
		return
	}
	m.segs = append(m.segs, seg{orig: m.OrigLen(), out: m.OutLen(), ulen: n})
}

// OrigLen is the total original length covered.
func (m *Mapper) OrigLen() int {
	if len(m.segs) == 0 {
		return 0
	}
	s := m.segs[len(m.segs)-1]
	return s.orig + s.olen
}

// OutLen is the total output length covered.
func (m *Mapper) OutLen() int {
	if len(m.segs) == 0 {
		return 0
	}
	s := m.segs[len(m.segs)-1]
	return s.out + s.ulen
}

// Segments reports the number of stored intervals.
func (m *Mapper) Segments() int { return len(m.segs) }

// LastChecks reports how many intervals the most recent ToOrig/ToOut
// binary search examined (probe count).
func (m *Mapper) LastChecks() int { return m.lastChecks }

// ToOrig maps an output offset o in [0, OutLen] to an original offset.
func (m *Mapper) ToOrig(o int) int {
	lo, hi := 0, len(m.segs)
	m.lastChecks = 0
	for lo < hi {
		mid := (lo + hi) / 2
		m.lastChecks++
		if m.segs[mid].out < o {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == len(m.segs) {
		return m.OrigLen()
	}
	s := m.segs[lo]
	if o < s.out {
		return s.orig
	}
	return s.orig + (o - s.out)
}

// ToOut maps an original offset i in [0, OrigLen] to an output offset.
// Deleted bytes map to the first retained output point after the deletion
// (or OutLen at EOF).
func (m *Mapper) ToOut(i int) int {
	lo, hi := 0, len(m.segs)
	m.lastChecks = 0
	for lo < hi {
		mid := (lo + hi) / 2
		m.lastChecks++
		if m.segs[mid].orig < i {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == len(m.segs) {
		return m.OutLen()
	}
	s := m.segs[lo]
	if i < s.orig || s.ulen == 0 {
		return s.out
	}
	return s.out + (i - s.orig)
}

// Truncate drops everything at or after output offset u, returning the
// corresponding original offset. Ending insertion/deletion segments are
// removed outright; a copied segment is shortened.
func (m *Mapper) Truncate(u int) int {
	for len(m.segs) > 0 {
		s := &m.segs[len(m.segs)-1]
		if s.out+s.ulen <= u {
			break
		}
		if s.out < u && s.olen == 0 {
			u = s.out
		}
		if s.out < u {
			c := u - s.out
			s.olen, s.ulen = c, c
			return s.orig + c
		}
		m.segs = m.segs[:len(m.segs)-1]
	}
	if len(m.segs) == 0 {
		return 0
	}
	s := m.segs[len(m.segs)-1]
	return s.orig + s.olen
}
