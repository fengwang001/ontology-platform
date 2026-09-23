// Package span records byte-offset correspondence between original input and
// normalized output and answers bidirectional offset queries by binary search.
package span

// Run is one maximal correspondence interval: original bytes [O0,O1) map to
// output bytes [U0,U1). A zero-width run is a deletion (orig only) or an
// insertion at end (out only).
type Run struct {
	O0, O1 int // original, half-open
	U0, U1 int // output, half-open
}

func (r Run) kind() int {
	switch {
	case r.O1-r.O0 == 0:
		return 0
	case r.U1-r.U0 == 0:
		return -1
	default:
		return 1
	}
}

// Table is an append-only interval table with merged adjacent runs.
type Table struct {
	runs []Run
	last int // intervals inspected by the most recent query
	OLen int
	ULen int
}

// Keep appends a preserved run of n bytes.
func (t *Table) Keep(n int) {
	if n <= 0 {
		return
	}
	if len(t.runs) > 0 {
		last := &t.runs[len(t.runs)-1]
		if last.kind() == 1 && last.O1 == t.OLen && last.U1 == t.ULen {
			last.O1 += n
			last.U1 += n
			t.OLen += n
			t.ULen += n
			return
		}
	}
	t.runs = append(t.runs, Run{t.OLen, t.OLen + n, t.ULen, t.ULen + n})
	t.OLen += n
	t.ULen += n
}

// Delete records n original bytes that produce no output.
func (t *Table) Delete(n int) {
	if n <= 0 {
		return
	}
	if len(t.runs) > 0 {
		last := &t.runs[len(t.runs)-1]
		if last.kind() == -1 && last.O1 == t.OLen && last.U0 == t.ULen && last.U1 == t.ULen {
			last.O1 += n
			t.OLen += n
			return
		}
	}
	t.runs = append(t.runs, Run{t.OLen, t.OLen + n, t.ULen, t.ULen})
	t.OLen += n
}

// Insert records n output bytes with no corresponding original bytes.
func (t *Table) Insert(n int) {
	if n <= 0 {
		return
	}
	if len(t.runs) > 0 {
		last := &t.runs[len(t.runs)-1]
		if last.kind() == 0 && last.O0 == t.OLen && last.O1 == t.OLen && last.U1 == t.ULen {
			last.U1 += n
			t.ULen += n
			return
		}
	}
	t.runs = append(t.runs, Run{t.OLen, t.OLen, t.ULen, t.ULen + n})
	t.ULen += n
}

// Runs returns a copy of the interval table.
func (t *Table) Runs() []Run { return append([]Run(nil), t.runs...) }

// LastChecks returns intervals inspected by the most recent query.
func (t *Table) LastChecks() int { return t.last }

// findRun returns the run index for original offset i using binary search.
func (t *Table) findRun(i int) int {
	t.last = 0
	lo, hi := 0, len(t.runs)
	for lo < hi {
		mid := (lo + hi) / 2
		t.last++
		if t.runs[mid].O1 <= i {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo < len(t.runs) {
		t.last++ // verify run covers the offset
	}
	return lo
}

// ToOut maps original offset i in [0,OLen] to an output offset (monotonic).
func (t *Table) ToOut(i int) int {
	if i >= t.OLen {
		t.last = 0
		return t.endToOut()
	}
	k := t.findRun(i)
	r := t.runs[k]
	if r.kind() == 1 {
		return r.U0 + (i - r.O0)
	}
	return r.U0 // deletion: successor output position
}

func (t *Table) endToOut() int {
	if len(t.runs) == 0 {
		return 0
	}
	last := t.runs[len(t.runs)-1]
	if last.kind() == 0 {
		return last.U0
	}
	return t.ULen
}

// ToOrig maps output offset o in [0,ULen] to an original offset (monotonic).
func (t *Table) ToOrig(o int) int {
	t.last = 0
	if o >= t.ULen {
		return t.endToOrig()
	}
	lo, hi := 0, len(t.runs)
	for lo < hi {
		mid := (lo + hi) / 2
		t.last++
		if t.runs[mid].U1 <= o {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	r := t.runs[lo]
	if r.kind() == 1 {
		return r.O0 + (o - r.U0)
	}
	if r.kind() == 0 {
		return r.O0 // insertion: preceding original position
	}
	return r.O1 // deletion: successor original position
}

func (t *Table) endToOrig() int {
	if len(t.runs) == 0 {
		return 0
	}
	last := t.runs[len(t.runs)-1]
	if last.kind() == 0 {
		return last.O0
	}
	return t.OLen
}

// Truncate drops content strictly after original offset o and output offset u.
func (t *Table) Truncate(o, u int) {
	for len(t.runs) > 0 {
		last := t.runs[len(t.runs)-1]
		if last.O0 <= o && last.U0 <= u && last.O1 <= o && last.U1 <= u {
			break
		}
		if last.O0 <= o && last.U0 <= u && last.kind() == 1 {
			cut := o - last.O0
			if u-last.U0 < cut {
				cut = u - last.U0
			}
			if cut < 0 {
				cut = 0
			}
			last.O1 = last.O0 + cut
			last.U1 = last.U0 + cut
			t.runs[len(t.runs)-1] = last
			break
		}
		t.runs = t.runs[:len(t.runs)-1]
	}
	t.OLen, t.ULen = o, u
}

// Shift returns a copy translated by original delta do and output delta du.
func (t *Table) Shift(do, du int) *Table {
	s := &Table{runs: make([]Run, len(t.runs)), OLen: t.OLen + do, ULen: t.ULen + du, last: 0}
	for i, r := range t.runs {
		s.runs[i] = Run{r.O0 + do, r.O1 + do, r.U0 + du, r.U1 + du}
	}
	return s
}

// Merge appends table s (already shifted) to t.
func (t *Table) Merge(s *Table) {
	for _, r := range s.runs {
		t.runs = append(t.runs, r)
	}
	t.OLen, t.ULen = s.OLen, s.ULen
}
