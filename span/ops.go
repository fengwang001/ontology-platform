package span

// Merge concatenates two maps, shifting b by original offset inBase and
// output offset outBase. Consecutive same-kind boundary runs merge.
func Merge(a, b *Map, inBase, outBase int) *Map {
	out := &Map{runs: make([]run, 0, len(a.runs)+len(b.runs))}
	for _, r := range a.runs {
		out.runs = append(out.runs, r)
	}
	for _, r := range b.runs {
		r.orig += inBase
		r.out += outBase
		out.runs = append(out.runs, r)
	}
	out.normalize()
	return out
}

// normalize merges adjacent runs of the same kind.
func (m *Map) normalize() {
	k := 0
	for i := 0; i < len(m.runs); i++ {
		r := m.runs[i]
		if k > 0 {
			p := &m.runs[k-1]
			same := (p.outLen > 0) == (r.outLen > 0)
			if same && p.origEnd() == r.orig && p.outEnd() == r.out {
				p.len += r.len
				p.outLen += r.outLen
				continue
			}
		}
		m.runs[k] = r
		k++
	}
	m.runs = m.runs[:k]
}

// DropHeadOne deletes the first original position that produced output
// (used when a segment-leading '\n' belongs to a cross-cut CRLF).
func (m *Map) DropHeadOne() {
	var done bool
	out := &Map{runs: make([]run, 0, len(m.runs)+1)}
	for _, r := range m.runs {
		if !done && r.outLen > 0 {
			out.Delete(1) // swallow the segment-leading byte
			out.Retain(r.len - 1)
			done = true
			continue
		}
		if r.outLen == 0 {
			out.Delete(r.len)
		} else {
			out.Retain(r.len)
		}
	}
	m.runs = out.runs
}

// KeepTailDropped turns the final deleted original run (must be exactly n
// bytes) into retained content, i.e. restores cross-cut trailing spaces.
func (m *Map) KeepTailDropped(n int) {
	if n <= 0 || len(m.runs) == 0 {
		return
	}
	i := len(m.runs) - 1
	r := &m.runs[i]
	if r.outLen != 0 || r.len != n {
		return
	}
	r.outLen = n
	m.normalize()
}

// FoldTail collapses the trailing-newline region [cutOrig, end) to a
// single output newline mapped onto the original newline at anchorOrig.
// Content before cutOrig keeps its recorded mapping; original bytes in
// the region other than anchor are recorded as deleted.
func (m *Map) FoldTail(cutOrig, anchorOrig int) *Map {
	out := &Map{runs: make([]run, 0, len(m.runs)+2)}
	for _, r := range m.runs {
		if r.origEnd() <= cutOrig {
			if r.outLen == 0 {
				out.Delete(r.len)
			} else {
				out.Retain(r.len)
			}
			continue
		}
		if r.orig < cutOrig {
			if r.outLen == 0 {
				out.Delete(cutOrig - r.orig)
			} else {
				out.Retain(cutOrig - r.orig)
			}
		}
		break
	}
	out.Delete(anchorOrig - cutOrig)
	out.Retain(1)
	out.Delete(m.OrigLen() - anchorOrig - 1)
	return out
}
