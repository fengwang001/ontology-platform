// Package span records the correspondence between original byte
// offsets and normalized-output byte offsets and answers both
// directions by binary search.
//
// A segment is an affine run from [Orig,Orig+OrigLen) to
// [Out,Out+OutLen): a surviving run has both lengths positive and
// equal slopes 1:1, a deleted run has OutLen 0, a purely appended
// run (e.g. a policy-added newline) has OrigLen 0. Consecutive
// runs of the same kind coalesce, so the table grows only with
// deletion/insertion points, never with output size.
package span

// Map is a single-user offset correspondence table.
type Map struct {
	s      []seg
	probes int
}

type seg struct {
	orig, out  int64
	olen, dlen int64
}

// New returns an empty map.
func New() *Map { return &Map{} }

// Survive records n bytes copied verbatim.
func (m *Map) Survive(n int64) {
	if n <= 0 {
		return
	}
	if k := len(m.s) - 1; k >= 0 && m.s[k].olen > 0 && m.s[k].dlen > 0 {
		m.s[k].olen += n
		m.s[k].dlen += n
		return
	}
	m.append(seg{orig: m.OrigLen(), out: m.OutLen(), olen: n, dlen: n})
}

// Delete records n original bytes that produced no output.
func (m *Map) Delete(n int64) {
	if n <= 0 {
		return
	}
	if k := len(m.s) - 1; k >= 0 && m.s[k].olen > 0 && m.s[k].dlen == 0 {
		m.s[k].olen += n
		return
	}
	m.append(seg{orig: m.OrigLen(), out: m.OutLen(), olen: n})
}

// Append records n output bytes with no original source.
func (m *Map) Append(n int64) {
	if n > 0 {
		m.append(seg{orig: m.OrigLen(), out: m.OutLen(), dlen: n})
	}
}

// TruncateOut rolls the map back so that its output ends at keep.
// keep must land inside a surviving run (used to trim an output tail
// that is still 1:1 with the original).
func (m *Map) TruncateOut(keep int64) {
	for len(m.s) > 0 {
		e := m.s[len(m.s)-1]
		if keep >= e.out+e.dlen {
			return
		}
		m.s = m.s[:len(m.s)-1]
		if keep > e.out {
			n := keep - e.out
			m.append(seg{orig: e.orig, out: e.out, olen: n, dlen: n})
			return
		}
	}
}

// Concat appends every segment of other after shifting its
// coordinates by this map's current extents, coalescing at the seam.
func (m *Map) Concat(other *Map) {
	oo, ou := m.OrigLen(), m.OutLen()
	for _, e := range other.s {
		e.orig += oo
		e.out += ou
		if len(m.s) > 0 {
			p := m.s[len(m.s)-1]
			if p.olen > 0 && p.dlen > 0 && e.olen > 0 && e.dlen > 0 {
				m.s[len(m.s)-1].olen += e.olen
				m.s[len(m.s)-1].dlen += e.dlen
				continue
			}
		}
		m.append(e)
	}
}

// ToOrig maps an output offset to an original offset.
func (m *Map) ToOrig(o int64) int64 {
	i := m.search(func(e seg) bool { return e.out > o })
	if i == 0 {
		return m.anchorOrig(0)
	}
	e := m.s[i-1]
	if e.dlen > 0 && o < e.out+e.dlen {
		return e.orig + (o - e.out)
	}
	if e.dlen == 0 && o == e.out && i < len(m.s) {
		return m.anchorOrig(i)
	}
	return e.orig
}

// ToOut maps an original offset to an output offset.
func (m *Map) ToOut(i int64) int64 {
	k := m.search(func(e seg) bool { return e.orig > i })
	if k == 0 {
		if len(m.s) > 0 && m.s[0].olen == 0 {
			return m.s[0].out
		}
		return 0
	}
	e := m.s[k-1]
	if e.olen > 0 && i < e.orig+e.olen {
		if e.dlen > 0 {
			return e.out + (i - e.orig)
		}
		return e.out
	}
	if k < len(m.s) {
		return m.s[k].out
	}
	return e.out + e.dlen
}

// Probes reports how many binary-search steps the last query used.
func (m *Map) Probes() int { return m.probes }

// Intervals reports the number of stored segments.
func (m *Map) Intervals() int { return len(m.s) }

// OrigLen is the total original extent.
func (m *Map) OrigLen() int64 {
	if len(m.s) == 0 {
		return 0
	}
	e := m.s[len(m.s)-1]
	return e.orig + e.olen
}

// OutLen is the total output extent.
func (m *Map) OutLen() int64 {
	if len(m.s) == 0 {
		return 0
	}
	e := m.s[len(m.s)-1]
	return e.out + e.dlen
}

func (m *Map) anchorOrig(k int) int64 {
	for ; k < len(m.s); k++ {
		if m.s[k].olen > 0 {
			return m.s[k].orig
		}
	}
	return m.s[len(m.s)-1].orig
}

func (m *Map) search(greater func(seg) bool) int {
	lo, hi, steps := 0, len(m.s), 0
	for lo < hi {
		steps++
		mid := int(uint(lo+hi) >> 1)
		if greater(m.s[mid]) {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	m.probes = steps
	return lo
}

func (m *Map) append(e seg) { m.s = append(m.s, e) }
