// Package span records source-to-output byte offset correspondences of a
// deleting-only normalizer and answers both directions by binary search.
package span

// Map is a piecewise-linear correspondence between strictly increasing
// anchor points. A gap is slope 1 (kept bytes) or slope 0 (deleted source
// bytes sharing one output cursor).
type Map struct {
	orig        []int
	out         []int
	lastChecked int // unexported: anchor comparisons used by the last query
}

// New starts a map anchored at (0,0).
func New() *Map { return &Map{orig: []int{0}, out: []int{0}} }

// Keep records n copied bytes.
func (m *Map) Keep(n int) {
	if n <= 0 {
		return
	}
	m.orig = append(m.orig, m.orig[len(m.orig)-1]+n)
	m.out = append(m.out, m.out[len(m.out)-1]+n)
}

// Drop records n deleted source bytes at the current output cursor.
func (m *Map) Drop(n int) {
	if n <= 0 {
		return
	}
	k := len(m.orig) - 1
	if k >= 1 && m.out[k] == m.out[k-1] {
		m.orig[k] += n // extend the flat gap's end anchor
		return
	}
	m.orig = append(m.orig, m.orig[k]+n) // start anchor is the last point
	m.out = append(m.out, m.out[k])
}

// Finish closes the map at the final source length.
func (m *Map) Finish(origLen int) {
	if n := origLen - m.orig[len(m.orig)-1]; n > 0 {
		m.Keep(n)
	}
}

// OrigLen and OutLen are the current extents.
func (m *Map) OrigLen() int { return m.orig[len(m.orig)-1] }
func (m *Map) OutLen() int  { return m.out[len(m.out)-1] }

// Segments is the number of stored anchors.
func (m *Map) Segments() int { return len(m.orig) }

// LastChecked reports anchor comparisons performed by the last query.
func (m *Map) LastChecked() int { return m.lastChecked }

// search returns the first anchor index whose position at axis is > v.
func (m *Map) search(axis []int, v int) int {
	lo, hi := 0, len(axis)
	for lo < hi {
		m.lastChecked++
		mid := int(uint(lo+hi) >> 1)
		if axis[mid] > v {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}

// ToOut maps a source offset in [0,origLen] to an output offset.
func (m *Map) ToOut(i int) int {
	m.lastChecked = 0
	k := m.search(m.orig, i)
	if k == 0 {
		return 0
	}
	lo := k - 1
	if m.out[lo] == m.out[lo+1] {
		return m.out[lo] // deleted run: map onto the gap's output cursor
	}
	return m.out[lo] + i - m.orig[lo]
}

// ToOrig maps an output offset in [0,outLen] to a source offset.
func (m *Map) ToOrig(o int) int {
	m.lastChecked = 0
	k := m.search(m.out, o)
	if k == 0 {
		return 0
	}
	lo := k - 1
	return m.orig[lo] + o - m.out[lo]
}

// Restrict truncates the map to source offsets < origEnd (slope-1 tail bytes
// are assumed at the boundary), then finishes at origEnd.
func (m *Map) Restrict(origEnd int) *Map {
	r := New()
	for k := 1; k < len(m.orig); k++ {
		if m.orig[k] <= origEnd {
			r.orig = append(r.orig, m.orig[k])
			r.out = append(r.out, m.out[k])
			continue
		}
		if o := r.OrigLen(); o < origEnd {
			r.Keep(origEnd - o)
		}
		break
	}
	return r
}

// RestrictOut truncates output so it ends at output cursor outEnd; a flat gap
// straddling outEnd is turned into a deletion up to that cursor.
func (m *Map) RestrictOut(outEnd int) *Map {
	r := New()
	for k := 1; k < len(m.orig); k++ {
		if m.out[k] < outEnd || (m.out[k] == outEnd && m.orig[k-1] != m.orig[k]) {
			r.orig = append(r.orig, m.orig[k])
			r.out = append(r.out, m.out[k])
			if m.out[k] == outEnd {
				break
			}
			continue
		}
		lo := k - 1
		r.orig = append(r.orig, m.orig[lo+1])
		r.out = append(r.out, outEnd)
		break
	}
	return r
}

// Merge appends b's anchors translated by (origShift,outShift).
func Merge(a, b *Map, origShift, outShift int) *Map {
	r := &Map{orig: append([]int(nil), a.orig...), out: append([]int(nil), a.out...)}
	for k := 1; k < len(b.orig); k++ {
		r.orig = append(r.orig, origShift+b.orig[k])
		r.out = append(r.out, outShift+b.out[k])
	}
	return r
}
