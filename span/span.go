// Package span records the correspondence between original and output
// byte offsets as a sorted list of retained runs, and answers bidirectional
// queries by binary search. Deleted original bytes are the gaps between
// runs, so the table grows only with the number of deletion points.
package span

// Run maps original [OrigLo, OrigHi) to output [OutLo, OutHi).
type Run struct {
	OrigLo, OrigHi, OutLo int
}

// OutHi is the exclusive output end of the run.
func (r Run) OutHi() int { return r.OutLo + r.OrigHi - r.OrigLo }

// Map is an immutable bidirectional offset mapping. Domains are [0, len)
// plus the endpoint len itself. Deleted original bytes map forward to the
// next retained output position (or outLen). A synthesized output byte
// (only ever one trailing '\n' added by policy) has ToOrig = origLen.
type Map struct {
	runs    []Run
	origLen int
	outLen  int
	synth   int
	checked int // runs inspected by the last ToOrig/ToOut
}

// ToOrig maps an output offset back to an original offset.
func (m *Map) ToOrig(o int) int {
	if o >= m.outLen {
		return m.origLen
	}
	lo, hi, n := 0, len(m.runs), 0
	for lo < hi {
		mid := (lo + hi) / 2
		n++
		if m.runs[mid].OutLo <= o {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	m.checked = n
	if lo == 0 {
		return 0
	}
	r := m.runs[lo-1]
	if o < r.OutHi() {
		return r.OrigLo + o - r.OutLo
	}
	return m.origLen // synthesized byte at end of output
}

// ToOut maps an original offset to an output offset.
func (m *Map) ToOut(i int) int {
	if i >= m.origLen {
		return m.outLen
	}
	lo, hi, n := 0, len(m.runs), 0
	for lo < hi {
		mid := (lo + hi) / 2
		n++
		if m.runs[mid].OrigHi > i {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	m.checked = n
	if lo == len(m.runs) {
		return m.outLen // deleted tail
	}
	r := m.runs[lo]
	if i >= r.OrigLo {
		return r.OutLo + i - r.OrigLo
	}
	return r.OutLo // deleted byte: forward to next retained position
}

// LastChecked returns how many runs the last query inspected.
func (m *Map) LastChecked() int { return m.checked }

// Runs returns the retained runs (sorted, non-overlapping).
func (m *Map) Runs() []Run { return m.runs }

// Len returns the number of retained runs.
func (m *Map) Len() int { return len(m.runs) }

// Synth returns the number of synthesized trailing output bytes.
func (m *Map) Synth() int { return m.synth }

// OrigLen returns the original length the map was built with.
func (m *Map) OrigLen() int { return m.origLen }

// OutLen returns the output length the map was built with.
func (m *Map) OutLen() int { return m.outLen }

// Builder accumulates runs in order and merges adjacent ones.
type Builder struct {
	runs  []Run
	out   int
	synth int
}

// Keep records that original [lo, hi) is retained at output outLo.
// Calls must be made in increasing original and output order.
func (b *Builder) Keep(lo, hi, outLo int) {
	if hi <= lo {
		return
	}
	if n := len(b.runs); n > 0 && b.runs[n-1].OrigHi == lo && b.runs[n-1].OutHi() == outLo {
		b.runs[n-1].OrigHi = hi
	} else {
		b.runs = append(b.runs, Run{OrigLo: lo, OrigHi: hi, OutLo: outLo})
	}
	b.out = outLo + hi - lo
}

// Synth records n synthesized output bytes with no original source.
func (b *Builder) Synth(n int) { b.synth += n; b.out += n }

// TruncateOut drops all output at offsets >= l, chopping runs.
func (b *Builder) TruncateOut(l int) {
	for len(b.runs) > 0 && b.runs[len(b.runs)-1].OutLo >= l {
		b.runs = b.runs[:len(b.runs)-1]
	}
	if n := len(b.runs); n > 0 && b.runs[n-1].OutHi() > l {
		b.runs[n-1].OrigHi -= b.runs[n-1].OutHi() - l
	}
	b.out = l
}

// OutLen returns the current output length.
func (b *Builder) OutLen() int { return b.out }

// Runs returns the accumulated runs.
func (b *Builder) Runs() []Run { return b.runs }

// Build freezes the builder into a Map with the given original length.
func (b *Builder) Build(origLen int) *Map {
	runs := make([]Run, len(b.runs))
	copy(runs, b.runs)
	return &Map{runs: runs, origLen: origLen, outLen: b.out, synth: b.synth}
}
