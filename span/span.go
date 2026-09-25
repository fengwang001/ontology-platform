// Package span maps between original and normalized byte offsets.
package span

import "sort"

// Run is one retained half-open interval [OrigStart, OrigStart+Length).
type Run struct {
	OrigStart int
	OutStart  int
	Length    int
}

// Map contains coalesced retained intervals.
type Map struct {
	runs []Run
	orig int
	out  int
	last int
}

// New returns an empty map.
func New() *Map { return &Map{} }

// Retain records length preserved bytes beginning at current ends.
func (m *Map) Retain(length int) {
	if length <= 0 {
		return
	}
	if n := len(m.runs); n > 0 && m.runs[n-1].OrigStart+m.runs[n-1].Length == m.orig && m.runs[n-1].OutStart+m.runs[n-1].Length == m.out {
		m.runs[n-1].Length += length
	} else {
		m.runs = append(m.runs, Run{OrigStart: m.orig, OutStart: m.out, Length: length})
	}
	m.orig += length
	m.out += length
}

// Delete records length removed original bytes.
func (m *Map) Delete(length int) {
	if length > 0 {
		m.orig += length
	}
}

// Append merges src after shifting its coordinates by the given global starts.
func (m *Map) Append(src *Map, origShift, outShift int) {
	for _, r := range src.runs {
		m.runs = append(m.runs, Run{r.OrigStart + origShift, r.OutStart + outShift, r.Length})
	}
	m.orig = origShift + src.orig
	m.out = outShift + src.out
	m.coalesce()
}

// OrigLen and OutLen return mapped coordinate-space lengths.
func (m *Map) OrigLen() int { return m.orig }
func (m *Map) OutLen() int  { return m.out }

// Runs returns the number of stored intervals; tests use it for size bounds.
func (m *Map) Runs() int { return len(m.runs) }

// Checks returns intervals examined by the latest query.
func (m *Map) Checks() int { return m.last }

// TrimOutput truncates the map after normalized output has been shortened.
func (m *Map) TrimOutput(length int) {
	for len(m.runs) > 0 {
		r := m.runs[len(m.runs)-1]
		if r.OutStart >= length {
			m.runs = m.runs[:len(m.runs)-1]
			continue
		}
		if r.OutStart+r.Length > length {
			cut := length - r.OutStart
			m.runs[len(m.runs)-1].Length = cut
		}
		break
	}
	m.out = length
}

func (m *Map) coalesce() {
	if len(m.runs) < 2 {
		return
	}
	keep := m.runs[:1]
	for _, r := range m.runs[1:] {
		p := &keep[len(keep)-1]
		if p.OrigStart+p.Length == r.OrigStart && p.OutStart+p.Length == r.OutStart {
			p.Length += r.Length
		} else {
			keep = append(keep, r)
		}
	}
	m.runs = keep
}

// ToOrig maps an output offset in [0, OutLen].
func (m *Map) ToOrig(o int) int {
	n := len(m.runs)
	k := sort.Search(n, func(i int) bool { return m.runs[i].OutStart+m.runs[i].Length >= o })
	m.last = logSteps(n) + 1
	if k == n {
		return m.orig
	}
	r := m.runs[k]
	if o < r.OutStart {
		return r.OrigStart
	}
	return r.OrigStart + (o - r.OutStart)
}

// ToOut maps an original offset in [0, OrigLen].
func (m *Map) ToOut(i int) int {
	n := len(m.runs)
	k := sort.Search(n, func(i2 int) bool { return m.runs[i2].OrigStart+m.runs[i2].Length >= i })
	m.last = logSteps(n) + 1
	if k == n {
		return m.out
	}
	r := m.runs[k]
	if i < r.OrigStart {
		return r.OutStart
	}
	return r.OutStart + (i - r.OrigStart)
}

func logSteps(n int) int {
	s := 0
	for n > 1 {
		n, s = (n+1)/2, s+1
	}
	return s
}
