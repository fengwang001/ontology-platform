// Package span records the correspondence between original byte offsets
// and normalized output byte offsets, and answers both directions by
// binary search.
package span

// Run is a length-preserving correspondence between a half-open original
// interval and a half-open output interval. Orig0 < 0 marks synthesized
// output bytes (a final newline added by EnsureOne).
type Run struct {
	Orig0, Orig1 int
	Out0, Out1   int
}

// Builder appends runs in increasing order.
type Builder struct {
	Runs    []Run
	origEnd int
	outEnd  int
}

// Keep records n preserved original bytes starting at original offset orig.
func (b *Builder) Keep(orig, n int) {
	if n <= 0 {
		return
	}
	if len(b.Runs) > 0 {
		last := &b.Runs[len(b.Runs)-1]
		if last.Orig1 == orig && last.Out1 == b.outEnd {
			last.Orig1 += n
			last.Out1 += n
			b.origEnd, b.outEnd = orig+n, b.outEnd+n
			return
		}
	}
	b.Runs = append(b.Runs, Run{orig, orig + n, b.outEnd, b.outEnd + n})
	b.origEnd, b.outEnd = orig+n, b.outEnd+n
}

// Delete records n removed original bytes starting at orig; they map onto
// the next output offset (a gap between runs).
func (b *Builder) Delete(orig, n int) { b.origEnd = orig + n }

// KeepOrigless records n output bytes with no original counterpart.
func (b *Builder) KeepOrigless(n int) {
	if n <= 0 {
		return
	}
	b.Runs = append(b.Runs, Run{-1, -1, b.outEnd, b.outEnd + n})
	b.outEnd += n
}

// TrimEnd removes the last k original bytes from the recorded tail.
func (b *Builder) TrimEnd(k int) {
	for k > 0 && len(b.Runs) > 0 {
		last := &b.Runs[len(b.Runs)-1]
		if last.Orig0 < 0 {
			b.outEnd = last.Out0
			b.Runs = b.Runs[:len(b.Runs)-1]
			continue
		}
		take := last.Orig1 - last.Orig0
		if take > k {
			take = k
		}
		last.Orig1 -= take
		last.Out1 -= take
		k -= take
		b.origEnd, b.outEnd = last.Orig1, last.Out1
		if last.Orig1 == last.Orig0 {
			b.Runs = b.Runs[:len(b.Runs)-1]
		}
	}
}

// Map freezes the recorded runs.
func (b *Builder) Map() *Map {
	runs := append([]Run(nil), b.Runs...)
	return &Map{runs: runs, origLen: b.origEnd, outLen: b.outEnd}
}

// Map is an immutable bidirectional offset map.
type Map struct {
	runs    []Run
	origLen int
	outLen  int
	probes  int
}

// NumRuns is the number of correspondence intervals.
func (m *Map) NumRuns() int { return len(m.runs) }

// OrigLen and OutLen are the inclusive endpoints (the extra len points).
func (m *Map) OrigLen() int { return m.origLen }
func (m *Map) OutLen() int  { return m.outLen }

// Probes is the number of intervals examined by the latest query.
func (m *Map) Probes() int { return m.probes }

// ToOrig maps an output offset to an original offset.
func (m *Map) ToOrig(o int) int {
	m.probes = 0
	if o >= m.outLen {
		return m.origLen
	}
	lo, hi := 0, len(m.runs)
	for lo < hi {
		mid := (lo + hi) / 2
		m.probes++
		r := m.runs[mid]
		if o < r.Out0 {
			hi = mid
		} else if o >= r.Out1 {
			lo = mid + 1
		} else {
			if r.Orig0 < 0 {
				if mid > 0 {
					return m.runs[mid-1].Orig1
				}
				return 0
			}
			return r.Orig0 + (o - r.Out0)
		}
	}
	if lo < len(m.runs) {
		if m.runs[lo].Orig0 < 0 {
			if lo > 0 {
				return m.runs[lo-1].Orig1
			}
			return 0
		}
		return m.runs[lo].Orig0
	}
	return m.origLen
}

// ToOut maps an original offset to an output offset.
func (m *Map) ToOut(i int) int {
	m.probes = 0
	if i >= m.origLen {
		return m.outLen
	}
	lo, hi := 0, len(m.runs)
	for lo < hi {
		mid := (lo + hi) / 2
		m.probes++
		r := m.runs[mid]
		if r.Orig0 < 0 || i < r.Orig0 {
			hi = mid
		} else if i >= r.Orig1 {
			lo = mid + 1
		} else {
			return r.Out0 + (i - r.Orig0)
		}
	}
	if lo < len(m.runs) {
		return m.runs[lo].Out0
	}
	return m.outLen
}

// Concat shifts the pieces' coordinates onto contiguous global axes and
// merges them. origShift/outShift are cumulative; origLen/outLen are the
// global total original and output lengths.
func Concat(pieces []*Map, origBounds, outBounds []int, origLen, outLen int) *Map {
	var runs []Run
	for k, p := range pieces {
		for _, r := range p.runs {
			nr := Run{r.Orig0 + origBounds[k], r.Orig1 + origBounds[k],
				r.Out0 + outBounds[k], r.Out1 + outBounds[k]}
			if r.Orig0 < 0 {
				nr.Orig0, nr.Orig1 = -1, -1
			}
			if len(runs) > 0 {
				last := &runs[len(runs)-1]
				if last.Orig1 == nr.Orig0 && last.Out1 == nr.Out0 && nr.Orig0 >= 0 {
					last.Orig1, last.Out1 = nr.Orig1, nr.Out1
					continue
				}
			}
			runs = append(runs, nr)
		}
	}
	return &Map{runs: runs, origLen: origLen, outLen: outLen}
}
