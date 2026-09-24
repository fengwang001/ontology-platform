// Package span records the correspondence between original and normalized
// byte offsets and answers both directions by binary search.
//
// Runs are disjoint 1:1 kept-byte intervals sorted by original offset.
// Bytes between runs were deleted; they map to the start of the following
// run. A synthetic final newline (added by an end policy, with no original
// byte) is recorded as tailOut: ToOrig(tailOut)==nIn.
package span

// Run is one 1:1 interval of length Len starting at Orig/Out.
type Run struct {
	Orig int
	Out  int
	Len  int
}

// Map is an offset correspondence. Zero value is empty.
type Map struct {
	runs    []Run
	nIn     int
	nOut    int
	tailOut int // synthetic appended newline, -1 if none
	checked int // intervals examined by the last query
}

// New returns an empty map.
func New() *Map { return &Map{tailOut: -1} }

// Add records a new 1:1 run; adjacent runs merge, so run count tracks delete
// points rather than output size.
func (m *Map) Add(orig, out, length int) {
	if length <= 0 {
		return
	}
	if n := len(m.runs); n > 0 {
		last := &m.runs[n-1]
		if last.Orig+last.Len == orig && last.Out+last.Len == out {
			last.Len += length
			m.nIn, m.nOut = orig+length, out+length
			return
		}
	}
	m.runs = append(m.runs, Run{orig, out, length})
	m.nIn, m.nOut = orig+length, out+length
}

// TrimTo drops every run starting at or after (origAt,outAt) and clears the
// synthetic tail. Used when the end policy deletes trailing newlines.
func (m *Map) TrimTo(origAt, outAt int) {
	idx := len(m.runs)
	for idx > 0 && m.runs[idx-1].Orig >= origAt && m.runs[idx-1].Out >= outAt {
		idx--
	}
	m.runs = m.runs[:idx]
	m.nIn, m.nOut, m.tailOut = origAt, outAt, -1
}

// SetTail records a synthetic newline at output offset out (== nOut-1).
func (m *Map) SetTail(out int) { m.tailOut = out }

// Runs reports how many intervals the map holds.
func (m *Map) Runs() int { return len(m.runs) }

// RunAt returns interval idx by value (for rebasing child maps).
func (m *Map) RunAt(idx int) Run { return m.runs[idx] }

// Checked reports intervals examined by the most recent ToOrig/ToOut call.
func (m *Map) Checked() int { return m.checked }

// searchLast returns the index of the last run whose start field <= pos,
// counting examined intervals (binary search only).
func (m *Map) searchLast(origSide bool, pos int) int {
	lo, hi, c := 0, len(m.runs), 0
	for lo < hi {
		mid := (lo + hi) / 2
		c++
		v := m.runs[mid].Out
		if origSide {
			v = m.runs[mid].Orig
		}
		if v <= pos {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	m.checked = c
	return lo - 1
}

// ToOut maps an original offset in [0,nIn] to an output offset.
func (m *Map) ToOut(i int) int {
	if i >= m.nIn {
		return m.nOut
	}
	r := m.searchLast(true, i)
	if r < 0 {
		return m.runs[0].Out
	}
	run := m.runs[r]
	if d := i - run.Orig; d < run.Len {
		return run.Out + d
	}
	if r+1 < len(m.runs) {
		return m.runs[r+1].Out // deleted gap -> next kept byte
	}
	return m.nOut
}

// ToOrig maps an output offset in [0,nOut] to an original offset.
func (m *Map) ToOrig(o int) int {
	if o >= m.nOut {
		return m.nIn
	}
	if o == m.tailOut {
		return m.nIn // synthetic appended newline
	}
	r := m.searchLast(false, o)
	if r < 0 {
		return m.runs[0].Orig
	}
	run := m.runs[r]
	if d := o - run.Out; d < run.Len {
		return run.Orig + d
	}
	if r+1 < len(m.runs) {
		return m.runs[r+1].Orig
	}
	return m.nIn
}
