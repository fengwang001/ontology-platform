// Package span records the correspondence between original byte ranges and
// output byte ranges and answers bidirectional offset queries by binary search.
package span

// Run is a kept (non-deleted) range: length bytes map 1:1 from origStart in
// the input to outStart in the output. A synthetic output byte (not coming
// from any input byte) has length 1 and origStart == OrigTotal.
type Run struct {
	OrigStart int
	OutStart  int
	Length    int
}

// Builder accumulates runs in stream order.
type Builder struct {
	runs       []Run
	origPos    int
	outPos     int
	origTotal  int
	querySteps int
}

func (b *Builder) last() *Run {
	if len(b.runs) == 0 {
		return nil
	}
	return &b.runs[len(b.runs)-1]
}

// Copy advances both cursors by n (kept bytes; merged with a prior copy).
func (b *Builder) Copy(n int) {
	if n <= 0 {
		return
	}
	if r := b.last(); r != nil && r.OrigStart+r.Length == b.origPos &&
		r.OutStart+r.Length == b.outPos && r.Length > 0 {
		r.Length += n
	} else {
		b.runs = append(b.runs, Run{OrigStart: b.origPos, OutStart: b.outPos, Length: n})
	}
	b.origPos += n
	b.outPos += n
	b.origTotal = b.origPos
}

// Drop advances only the original cursor by n (deleted bytes form a gap).
func (b *Builder) Drop(n int) {
	b.origPos += n
	b.origTotal = b.origPos
}

// Synthetic appends an output byte with no originating input byte.
func (b *Builder) Synthetic() {
	b.runs = append(b.runs, Run{OrigStart: b.origPos, OutStart: b.outPos, Length: 1})
	b.outPos++
}

// AddRun inserts an already globalized run (used by the parallel splicer).
func (b *Builder) AddRun(r Run) {
	if r.Length > 0 {
		b.runs = append(b.runs, r)
		if r.OrigStart >= b.origTotal {
			b.origTotal = r.OrigStart
		}
	}
}

// Runs returns the recorded runs (sorted by OutStart).
func (b *Builder) Runs() []Run { return b.runs }

// QuerySteps reports how many intervals the last query inspected.
func (b *Builder) QuerySteps() int { return b.querySteps }

// ToOrig maps an output offset in [0, OutLen] to an original offset.
func (b *Builder) ToOrig(o int) int {
	b.querySteps = 0
	runs := b.runs
	if len(runs) == 0 {
		return b.origTotal
	}
	lo, hi := 0, len(runs)
	for lo < hi {
		b.querySteps++
		mid := (lo + hi) / 2
		r := runs[mid]
		if o < r.OutStart {
			hi = mid
		} else if o >= r.OutStart+r.Length {
			lo = mid + 1
		} else {
			return r.OrigStart + (o - r.OutStart)
		}
	}
	b.querySteps++
	if lo >= len(runs) {
		return b.origTotal
	}
	return runs[lo].OrigStart
}

// ToOut maps an original offset in [0, OrigLen] to an output offset.
func (b *Builder) ToOut(i int) int {
	b.querySteps = 0
	runs := b.runs
	if len(runs) == 0 {
		return 0
	}
	lo, hi := 0, len(runs)
	for lo < hi {
		b.querySteps++
		mid := (lo + hi) / 2
		r := runs[mid]
		if i < r.OrigStart {
			hi = mid
		} else if r.Length > 0 && i >= r.OrigStart+r.Length {
			lo = mid + 1
		} else {
			return r.OutStart + (i - r.OrigStart)
		}
}
	b.querySteps++
	if lo >= len(runs) {
		return runs[len(runs)-1].OutStart + runs[len(runs)-1].Length
	}
	return runs[lo].OutStart
}
