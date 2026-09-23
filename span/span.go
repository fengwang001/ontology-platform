// Package span records byte-offset correspondence between an original
// buffer and a transformed output buffer, with bidirectional queries.
package span

// Kind identifies how an input interval relates to an output interval.
type Kind uint8

const (
	Copy   Kind = iota // input bytes copied 1:1 to output
	Delete             // input bytes removed
	Insert             // output bytes with no original source
)

// Run is one correspondence interval. Half-open coordinates are global.
// For Delete, OEnd==OStart; for Insert, IEnd==IStart.
type Run struct {
	Kind   Kind
	IStart int
	IEnd   int
	OStart int
	OEnd   int
}

// Map is a run-length encoded, strictly ordered list of runs that
// covers [0,lenInput) and [0,lenOutput).
type Map struct {
	runs    []Run
	checked int // runs examined by the most recent query
}

// New builds a map from runs, merging adjacent runs of the same kind.
func New(runs []Run) *Map {
	m := &Map{}
	for _, r := range runs {
		if n := len(m.runs); n > 0 && m.runs[n-1].Kind == r.Kind {
			prev := &m.runs[n-1]
			prev.IEnd, prev.OEnd = r.IEnd, r.OEnd
			continue
		}
		m.runs = append(m.runs, r)
	}
	return m
}

// Runs returns the underlying runs (shared).
func (m *Map) Runs() []Run { return m.runs }

// LenInput is the covered original length.
func (m *Map) LenInput() int {
	if len(m.runs) == 0 {
		return 0
	}
	return m.runs[len(m.runs)-1].IEnd
}

// LenOutput is the covered output length.
func (m *Map) LenOutput() int {
	if len(m.runs) == 0 {
		return 0
	}
	return m.runs[len(m.runs)-1].OEnd
}

// LastChecked reports how many runs the last query examined.
func (m *Map) LastChecked() int { return m.checked }

// ToOrig maps an output offset o in [0,lenOutput] to an original offset.
// Boundaries resolve to the run on the right, so every result points at
// a surviving byte or at the final endpoint.
func (m *Map) ToOrig(o int) int {
	m.checked = 0
	lo, hi := 0, len(m.runs)
	for lo < hi {
		mid := (lo + hi) / 2
		m.checked++
		if m.runs[mid].OEnd <= o {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo >= len(m.runs) {
		return m.LenInput()
	}
	r := m.runs[lo]
	if r.Kind == Copy && o == r.OEnd && lo+1 < len(m.runs) && m.runs[lo+1].Kind == Insert {
		r = m.runs[lo+1]
	}
	if r.Kind == Insert {
		return r.IStart
	}
	if r.Kind == Copy {
		return r.IStart + (o - r.OStart)
	}
	return r.IEnd
}

// ToOut maps an original offset i in [0,lenInput] to an output offset.
// Offsets inside a deleted run resolve to that run's output endpoint
// (the following newline boundary; lenOutput when trailing).
func (m *Map) ToOut(i int) int {
	m.checked = 0
	lo, hi := 0, len(m.runs)
	for lo < hi {
		mid := (lo + hi) / 2
		m.checked++
		if m.runs[mid].IEnd <= i {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo >= len(m.runs) {
		return m.LenOutput()
	}
	r := m.runs[lo]
	if r.Kind == Insert {
		return r.OStart
	}
	if r.Kind == Copy {
		return r.OStart + (i - r.IStart)
	}
	return r.OEnd
}

// Translate shifts every coordinate by di (input) and do (output).
func Translate(r Run, di, do int) Run {
	r.IStart += di
	r.IEnd += di
	r.OStart += do
	r.OEnd += do
	return r
}
