// Package span records the correspondence between original byte ranges and
// output byte ranges and answers bidirectional offset queries by binary search.
package span

import "errors"

// Seg is one mapping segment: original [OrigStart, OrigStart+OrigLen) maps to
// output [OutStart, OutStart+OutLen). A kept segment has equal lengths; a
// deleted segment has OutLen 0; a synthetic segment has OrigLen 0.
type Seg struct {
	OrigStart int
	OutStart  int
	OrigLen   int
	OutLen    int
}

// Table is an ordered, contiguous list of mapping segments.
type Table struct {
	segs      []Seg
	origTotal int
	outTotal  int
	// probeCount, unexported, counts segments inspected by the most recent
	// ToOrig/ToOut query (binary-search loop iterations).
	probeCount int
}

// ErrOutOfRange is returned for offsets outside the closed [0,len] domain.
var ErrOutOfRange = errors.New("span: offset out of range")

// Append adds a segment, merging with the previous one when both are kept or
// both deleted runs sharing a boundary.
func (t *Table) Append(origStart, outStart, origLen, outLen int) {
	if n := len(t.segs); n > 0 {
		p := &t.segs[n-1]
		sameKind := (p.OrigLen == p.OutLen) == (origLen == outLen) &&
			(origLen > 0 || outLen > 0)
		if sameKind && p.OrigStart+p.OrigLen == origStart &&
			p.OutStart+p.OutLen == outStart {
			p.OrigLen += origLen
			p.OutLen += outLen
			t.origTotal += origLen
			t.outTotal += outLen
			return
		}
	}
	t.segs = append(t.segs, Seg{origStart, outStart, origLen, outLen})
	t.origTotal += origLen
	t.outTotal += outLen
}

// Segments returns the underlying segments (for tests and par composition).
func (t *Table) Segments() []Seg { return t.segs }

// OrigLen is the total original length.
func (t *Table) OrigLen() int { return t.origTotal }

// OutLen is the total output length.
func (t *Table) OutLen() int { return t.outTotal }

// LastProbe returns segments inspected by the most recent query.
func (t *Table) LastProbe() int { return t.probeCount }

// ToOrig maps an output offset to an original offset.
func (t *Table) ToOrig(o int) (int, error) {
	if o < 0 || o > t.outTotal {
		return 0, ErrOutOfRange
	}
	lo, hi := 0, len(t.segs)
	for lo < hi {
		t.probeCount++
		m := (lo + hi) / 2
		s := t.segs[m]
		if o < s.OutStart {
			hi = m
		} else if o >= s.OutStart+s.OutLen {
			lo = m + 1
		} else {
			return s.OrigStart + (o - s.OutStart), nil
		}
	}
	if lo > 0 {
		return t.segs[lo-1].OrigStart + t.segs[lo-1].OrigLen, nil
	}
	return 0, nil
}

// ToOut maps an original offset to an output offset.
func (t *Table) ToOut(i int) (int, error) {
	if i < 0 || i > t.origTotal {
		return 0, ErrOutOfRange
	}
	lo, hi := 0, len(t.segs)
	for lo < hi {
		t.probeCount++
		m := (lo + hi) / 2
		s := t.segs[m]
		if i < s.OrigStart {
			hi = m
		} else if i >= s.OrigStart+s.OrigLen {
			lo = m + 1
		} else {
			return s.OutStart, nil
		}
	}
	if lo > 0 {
		return t.segs[lo-1].OutStart + t.segs[lo-1].OutLen, nil
	}
	return 0, nil
}

// Trim cuts the table back to output boundary p (0 <= p <= OutLen). Original
// bytes mapped at or beyond the boundary become deleted segments: the original
// domain is preserved in full, they just collapse onto output offset p.
func (t *Table) Trim(p int) {
	lo, hi := 0, len(t.segs)
	for lo < hi {
		m := (lo + hi) / 2
		s := t.segs[m]
		if p < s.OutStart {
			hi = m
		} else if p >= s.OutStart+s.OutLen {
			lo = m + 1
		} else {
			hi = m
			break
		}
	}
	t.outTotal = p
	// Split the boundary segment: a kept prefix of length keep, then the rest
	// of its original bytes become a deleted segment; following segments all
	// become deleted (their output shrank to nothing) or synthetic (dropped).
	if lo < len(t.segs) {
		s := t.segs[lo]
		keep := p - s.OutStart
		head := make([]Seg, 0, len(t.segs)+1)
		head = append(head, t.segs[:lo]...)
		if keep > 0 {
			head = append(head, Seg{s.OrigStart, s.OutStart, keep, keep})
		}
		if s.OrigLen-keep > 0 {
			head = append(head, Seg{s.OrigStart + keep, p, s.OrigLen - keep, 0})
		}
		for _, r := range t.segs[lo+1:] {
			if r.OrigLen > 0 {
				head = append(head, Seg{r.OrigStart, p, r.OrigLen, 0})
			}
		}
		t.segs = head
	}
}
