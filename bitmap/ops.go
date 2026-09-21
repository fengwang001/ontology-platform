package bitmap

// stopMode controls when the merge loop may terminate early.
type stopMode int

const (
	// stopWhenBothDone: union — run until both inputs are exhausted.
	stopWhenBothDone stopMode = iota
	// stopWhenEitherDone: intersect — once one input is exhausted
	// the remaining output is all zeros.
	stopWhenEitherDone
	// stopWhenLeftDone: difference — once the left input is
	// exhausted the remaining output is all zeros.
	stopWhenLeftDone
)

// cursor walks one run list. Positions beyond the last run read as
// an infinite supply of 0-bits.
type cursor struct {
	runs []run
	idx  int
	off  uint64 // offset within the current run
}

func (c *cursor) done() bool { return c.idx >= len(c.runs) }

func (c *cursor) val() uint8 {
	if c.done() {
		return 0
	}
	return c.runs[c.idx].val
}

// rem returns the remaining length of the current run, or 0 when
// the cursor is exhausted.
func (c *cursor) rem() uint64 {
	if c.done() {
		return 0
	}
	return c.runs[c.idx].length - c.off
}

// advance consumes n bits and reports whether the cursor moved into
// a new run.
func (c *cursor) advance(n uint64) bool {
	if c.done() {
		return false
	}
	c.off += n
	if c.off >= c.runs[c.idx].length {
		c.idx++
		c.off = 0
		return true
	}
	return false
}

// merge combines two run lists on the compressed domain using two
// cursors. op computes the output bit from the two input bits.
// steps counts how many times either cursor advanced into a new
// run. The inputs are never expanded into per-bit form.
func merge(a, b []run, op func(x, y uint8) uint8, stop stopMode) ([]run, uint64) {
	ca, cb := cursor{runs: a}, cursor{runs: b}
	var out []run
	var steps uint64
	for {
		switch stop {
		case stopWhenBothDone:
			if ca.done() && cb.done() {
				return normalize(out), steps
			}
		case stopWhenEitherDone:
			if ca.done() || cb.done() {
				return normalize(out), steps
			}
		case stopWhenLeftDone:
			if ca.done() {
				return normalize(out), steps
			}
		}
		// Consume the smaller of the two remaining run lengths.
		n := ca.rem()
		if r := cb.rem(); !cb.done() && (ca.done() || r < n) {
			n = r
		}
		v := op(ca.val(), cb.val())
		if m := len(out); m > 0 && out[m-1].val == v {
			out[m-1].length += n
		} else {
			out = append(out, run{val: v, length: n})
		}
		if ca.advance(n) {
			steps++
		}
		if cb.advance(n) {
			steps++
		}
	}
}

// Union returns the set union of b and o, computed directly on the
// run representation. steps reports how many run advances the merge
// performed.
func (b *Bitmap) Union(o *Bitmap) (result *Bitmap, steps uint64) {
	return b.mergeWith(o, func(x, y uint8) uint8 { return x | y }, stopWhenBothDone)
}

// Intersect returns the set intersection of b and o, computed
// directly on the run representation.
func (b *Bitmap) Intersect(o *Bitmap) (result *Bitmap, steps uint64) {
	return b.mergeWith(o, func(x, y uint8) uint8 { return x & y }, stopWhenEitherDone)
}

// Difference returns the set of elements in b but not in o,
// computed directly on the run representation.
func (b *Bitmap) Difference(o *Bitmap) (result *Bitmap, steps uint64) {
	return b.mergeWith(o, func(x, y uint8) uint8 { return x &^ y }, stopWhenLeftDone)
}

func (b *Bitmap) mergeWith(o *Bitmap, op func(x, y uint8) uint8, stop stopMode) (*Bitmap, uint64) {
	aRuns := b.runsSnapshot()
	var bRuns []run
	if o == b {
		bRuns = aRuns
	} else {
		bRuns = o.runsSnapshot()
	}
	merged, steps := merge(aRuns, bRuns, op, stop)
	return &Bitmap{runs: merged, totalLen: totalOf(merged)}, steps
}
