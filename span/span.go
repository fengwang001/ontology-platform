// Package span records correspondence between original byte ranges and
// output byte ranges and answers bidirectional offset queries.
//
// An entry is either a kept run (equal-length [Orig0,Orig1) <-> [Out0,Out1))
// or a deletion ([Orig0,Orig1) removed at output point Out0).
package span

// Entry is one correspondence interval.
type Entry struct {
	Orig0, Orig1 int
	Out0, Out1   int
	Del          bool
}

// Map is an append-only correspondence table built while streaming.
type Map struct {
	e          []Entry
	origLen    int
	lastChecks int
}

// New returns an empty map.
func New() *Map { return &Map{} }

// Keep appends a kept run of n bytes, advancing both coordinates.
func (m *Map) Keep(n int) {
	if n <= 0 {
		return
	}
	o0, i0 := m.end()
	if len(m.e) > 0 {
		last := &m.e[len(m.e)-1]
		if !last.Del && last.Orig1 == i0 && last.Out1 == o0 {
			last.Orig1 += n
			last.Out1 += n
			return
		}
	}
	m.e = append(m.e, Entry{i0, i0 + n, o0, o0 + n, false})
}

// Delete records original [start,end) as removed at current output point.
func (m *Map) Delete(start, end int) {
	if end <= start {
		return
	}
	o, _ := m.end()
	if n := len(m.e); n > 0 {
		if last := &m.e[n-1]; last.Del && last.Out0 == o && last.Orig1 == start {
			last.Orig1 = end
			return
		}
	}
	m.e = append(m.e, Entry{start, end, o, o, true})
}

// PopLastByte removes the final kept byte from the table (used to retract a
// trailing newline) and reports its original offset.
func (m *Map) PopLastByte() (origOff int, ok bool) {
	n := len(m.e)
	if n == 0 {
		return 0, false
	}
	last := &m.e[n-1]
	if last.Del || last.Orig1-last.Orig0 != 1 {
		return 0, false
	}
	origOff = last.Orig0
	last.Orig1--
	last.Out1--
	if last.Orig0 == last.Orig1 {
		m.e = m.e[:n-1]
	}
	if n2 := len(m.e); n2 >= 2 {
		a, b := &m.e[n2-2], &m.e[n2-1]
		if !a.Del && !b.Del && a.Orig1 == b.Orig0 && a.Out1 == b.Out0 {
			a.Orig1 = b.Orig1
			a.Out1 = b.Out1
			m.e = m.e[:n2-1]
		}
	}
	return origOff, true
}

// AppendNewline records a synthesized '\n' at the end with no original byte.
func (m *Map) AppendNewline() {
	o, i := m.end()
	m.e = append(m.e, Entry{i, i, o, o + 1, false})
}

// Finish declares the total original length; call once when the stream ends.
func (m *Map) Finish(origLen int) { m.origLen = origLen }

// Entries returns the intervals for splicing (par package).
func (m *Map) Entries() []Entry { return m.e }

// FromEntries builds a map from already-global entries and original length.
func FromEntries(e []Entry, origLen int) *Map { return &Map{e: e, origLen: origLen} }

// Checks returns intervals inspected by the most recent ToOrig/ToOut query.
func (m *Map) Checks() int { return m.lastChecks }

func (m *Map) end() (out, orig int) {
	if len(m.e) == 0 {
		return 0, 0
	}
	last := &m.e[len(m.e)-1]
	return last.Out1, last.Orig1
}

func clamp(v, hi int) int {
	if v < 0 {
		return 0
	}
	if v > hi {
		return hi
	}
	return v
}

// ToOrig maps an output offset to an original offset.
func (m *Map) ToOrig(o int) int {
	o = clamp(o, m.OutLen())
	lo, hi, c := 0, len(m.e), 0
	for lo < hi {
		mid := (lo + hi) / 2
		c++
		if m.e[mid].Out1 <= o {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	m.lastChecks = c
	if lo == len(m.e) {
		return m.origLen
	}
	e := &m.e[lo]
	if e.Del || o < e.Out0 {
		return e.Orig0
	}
	return e.Orig0 + (o - e.Out0)
}

// ToOut maps an original offset to an output offset.
// Offsets inside a deleted run map to the output point right after it.
func (m *Map) ToOut(i int) int {
	i = clamp(i, m.origLen)
	lo, hi, c := 0, len(m.e), 0
	for lo < hi {
		mid := (lo + hi) / 2
		c++
		if m.e[mid].Orig1 <= i {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	m.lastChecks = c
	if lo == len(m.e) {
		return m.OutLen()
	}
	e := &m.e[lo]
	if e.Del || i < e.Orig0 {
		return e.Out0
	}
	return e.Out0 + (i - e.Orig0)
}

// OutLen returns current output length.
func (m *Map) OutLen() int {
	if len(m.e) == 0 {
		return 0
	}
	return m.e[len(m.e)-1].Out1
}

// OrigLen returns declared original length.
func (m *Map) OrigLen() int { return m.origLen }
