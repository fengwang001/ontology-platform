package ontology

// Set marks bit v as a member of the set. It is idempotent: setting a bit
// that is already set changes nothing, including the encoding.
func (b *Bitmap) Set(v uint32) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.flipLocked(uint64(v), true)
}

// Clear removes bit v from the set. It is idempotent: clearing a bit that
// is already clear changes nothing, including the encoding.
func (b *Bitmap) Clear(v uint32) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.flipLocked(uint64(v), false)
}

// Contains reports whether bit v is a member of the set.
func (b *Bitmap) Contains(v uint32) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	i := b.findLocked(uint64(v))
	return i < len(b.runs) && b.runs[i].one
}

// flipLocked sets or clears the single bit at absolute position pos by
// splitting the covering run and re-normalizing. Callers must hold b.mu.
func (b *Bitmap) flipLocked(pos uint64, toOne bool) {
	i := b.findLocked(pos)
	if i == len(b.runs) {
		// Position lies in the implicit trailing zero region.
		if !toOne {
			return
		}
		var lastEnd uint64
		if n := len(b.ends); n > 0 {
			lastEnd = b.ends[n-1]
		}
		if gap := pos - lastEnd; gap > 0 {
			b.runs = append(b.runs, run{one: false, length: gap})
		}
		b.runs = append(b.runs, run{one: true, length: 1})
		b.normalizeLocked()
		return
	}
	r := b.runs[i]
	if r.one == toOne {
		return // idempotent no-op
	}
	start := b.runStartLocked(i)
	end := b.ends[i] // exclusive
	seg := make([]run, 0, 3)
	if pos > start {
		seg = append(seg, run{one: r.one, length: pos - start})
	}
	seg = append(seg, run{one: toOne, length: 1})
	if end-pos-1 > 0 {
		seg = append(seg, run{one: r.one, length: end - pos - 1})
	}
	next := make([]run, 0, len(b.runs)+2)
	next = append(next, b.runs[:i]...)
	next = append(next, seg...)
	next = append(next, b.runs[i+1:]...)
	b.runs = next
	b.normalizeLocked()
}

// SetRange marks every bit in [lo, hi] (inclusive) as a member of the set.
// It is a convenience for bulk construction; the result is normalized
// exactly as if each bit had been Set individually.
func (b *Bitmap) SetRange(lo, hi uint32) {
	if lo > hi {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.setRangeLocked(uint64(lo), uint64(hi)+1) // half-open [lo, hi+1)
}

// setRangeLocked inserts a one-run covering half-open [start, end).
// Callers must hold b.mu.
func (b *Bitmap) setRangeLocked(start, end uint64) {
	if start >= end {
		return
	}
	// Split the runs covering start and end-1 (if any), drop everything
	// strictly inside, and splice in a single one-run; normalize merges.
	i := b.findLocked(start)
	j := b.findLocked(end - 1)
	next := make([]run, 0, len(b.runs)+3)
	next = append(next, b.runs[:min(i, len(b.runs))]...)
	if i == len(b.runs) {
		var lastEnd uint64
		if n := len(b.ends); n > 0 {
			lastEnd = b.ends[n-1]
		}
		if gap := start - lastEnd; gap > 0 {
			next = append(next, run{one: false, length: gap})
		}
	}
	if i < len(b.runs) {
		if s := b.runStartLocked(i); s < start {
			next = append(next, run{one: b.runs[i].one, length: start - s})
		}
	}
	next = append(next, run{one: true, length: end - start})
	if j < len(b.runs) {
		if e := b.ends[j]; e > end {
			next = append(next, run{one: b.runs[j].one, length: e - end})
		}
		next = append(next, b.runs[j+1:]...)
	}
	b.runs = next
	b.normalizeLocked()
}
