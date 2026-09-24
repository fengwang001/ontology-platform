// Package span records the correspondence between original byte ranges
// and normalized-output byte ranges. Deleted original bytes map to the
// output boundary following their position. Both directions are answered
// by binary search over run starts.
package span

// run is either a retained segment (outLen==len) or a deleted original
// run (outLen==0). Consecutive same-kind runs are merged.
type run struct {
	orig, out, len, outLen int
}

func (r run) origEnd() int { return r.orig + r.len }
func (r run) outEnd() int  { return r.out + r.outLen }

// Map is an append-only correspondence table; not safe for concurrency.
type Map struct {
	runs        []run
	lastLookups int
}

func (m *Map) OrigLen() int {
	if len(m.runs) == 0 {
		return 0
	}
	return m.runs[len(m.runs)-1].origEnd()
}

func (m *Map) OutLen() int {
	if len(m.runs) == 0 {
		return 0
	}
	return m.runs[len(m.runs)-1].outEnd()
}

// Runs reports the recorded run count (grows only with deletions).
func (m *Map) Runs() int { return len(m.runs) }

// LastLookups reports runs examined by the most recent directional query.
func (m *Map) LastLookups() int { return m.lastLookups }

// Retain appends a kept segment present in both coordinates.
func (m *Map) Retain(n int) {
	if n <= 0 {
		return
	}
	if k := len(m.runs) - 1; k >= 0 && m.runs[k].outLen > 0 {
		m.runs[k].len += n
		m.runs[k].outLen += n
		return
	}
	m.runs = append(m.runs, run{orig: m.OrigLen(), out: m.OutLen(), len: n, outLen: n})
}

// Delete appends a deleted original run.
func (m *Map) Delete(n int) {
	if n <= 0 {
		return
	}
	if k := len(m.runs) - 1; k >= 0 && m.runs[k].outLen == 0 {
		m.runs[k].len += n
		return
	}
	m.runs = append(m.runs, run{orig: m.OrigLen(), out: m.OutLen(), len: n})
}

// findRun returns the run whose [start,end) in the chosen coordinate
// contains p, or the zero run when p is a terminal/closing boundary.
func (m *Map) findRun(p int, start, end func(run) int) (run, bool) {
	lo, hi := 0, len(m.runs)
	m.lastLookups = 0
	for lo < hi {
		mid := (lo + hi) / 2
		m.lastLookups++
		r := m.runs[mid]
		switch {
		case p < start(r):
			hi = mid
		case p >= end(r):
			lo = mid + 1
		default:
			return r, true
		}
	}
	m.lastLookups++
	return run{}, false
}

// ToOrig maps an output offset o in [0,OutLen] to an original offset.
func (m *Map) ToOrig(o int) int {
	if len(m.runs) == 0 {
		return o
	}
	if o >= m.OutLen() {
		return m.OrigLen()
	}
	r, ok := m.findRun(o, func(r run) int { return r.out }, func(r run) int { return r.outEnd() })
	if !ok {
		return m.OrigLen()
	}
	return r.orig + (o - r.out)
}

// ToOut maps an original offset i in [0,OrigLen] to an output offset.
// Offsets inside a deleted run map to the run's following output boundary.
func (m *Map) ToOut(i int) int {
	if len(m.runs) == 0 {
		return i
	}
	if i >= m.OrigLen() {
		return m.OutLen()
	}
	r, ok := m.findRun(i, func(r run) int { return r.orig }, func(r run) int { return r.origEnd() })
	if !ok {
		return m.OutLen()
	}
	if r.outLen == 0 {
		return r.out
	}
	return r.out + (i - r.orig)
}
