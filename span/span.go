// Package span records original-to-output byte offset correspondence. Only
// deleted original ranges are stored, so interval count grows with deletion
// points, not output size. All queries are binary searches; Checks exposes how
// many intervals the latest query examined.
package span

// Map stores deleted original ranges in ascending order (adjacent merged).
type Map struct {
	origLen    int
	del        [][2]int // [start,end)
	pref       []int    // pref[k] = deleted bytes among the first k ranges
	outLen     int
	lastChecks int
}

// New returns a map whose original coordinates start at orig.
func New(orig int) *Map { return &Map{origLen: orig} }

// Delete records that original [s,e) is removed.
func (m *Map) Delete(s, e int) {
	if e <= s {
		return
	}
	if n := len(m.del); n > 0 && m.del[n-1][1] == s {
		m.del[n-1][1] = e
	} else {
		m.del = append(m.del, [2]int{s, e})
		m.pref = append(m.pref, 0)
	}
	k := len(m.del) - 1
	prev := 0
	if k > 0 {
		prev = m.pref[k-1]
	}
	m.pref[k] = prev + (m.del[k][1] - m.del[k][0])
}

// Finish sets total lengths after normalization.
func (m *Map) Finish(origLen, outLen int) { m.origLen, m.outLen = origLen, outLen }

// Len returns the stored interval count.
func (m *Map) Len() int { return len(m.del) }

// Checks returns intervals examined by the latest ToOrig/ToOut query.
func (m *Map) Checks() int { return m.lastChecks }

// Append merges o into m, shifting original coordinates by origShift.
// Maps must be appended in ascending original order.
func (m *Map) Append(o *Map, origShift int) {
	for _, r := range o.del {
		m.Delete(r[0]+origShift, r[1]+origShift)
	}
}

// Ranges returns the stored deleted ranges in original coordinates.
func (m *Map) Ranges() [][2]int { return m.del }

func (m *Map) prefAt(k int) int {
	if k <= 0 {
		return 0
	}
	return m.pref[k-1]
}

// ToOrig maps output offset o (in [0,outLen]) to an original offset.
func (m *Map) ToOrig(o int) int {
	if o == m.outLen {
		m.lastChecks = 1
		return m.origLen
	}
	lo, hi, checks := 0, len(m.del), 1
	for lo < hi {
		mid := (lo + hi) / 2
		checks++
		if o+m.prefAt(mid) >= m.del[mid][1] {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	m.lastChecks = checks
	return o + m.prefAt(lo)
}

// ToOut maps original offset i (in [0,origLen]) to an output offset. A deleted
// position maps to the output offset of the next surviving byte.
func (m *Map) ToOut(i int) int {
	if i == m.origLen {
		m.lastChecks = 1
		return m.outLen
	}
	lo, hi, checks := 0, len(m.del), 1
	for lo < hi {
		mid := (lo + hi) / 2
		checks++
		if m.del[mid][1] <= i {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	m.lastChecks = checks
	return i - m.prefAt(lo)
}
