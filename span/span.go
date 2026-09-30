// Package span records correspondences between original byte ranges and output
// byte ranges and answers both directions by binary search.
//
// The table is a list of non-overlapping entries: identity entries map a run of
// equal length one-to-one (the bytes need not be equal, so synthesized newlines
// count as identity too); delete entries map a removed run to a single output
// point (the start of the next run, or the output end). Entries grow only with
// the number of deletion points, never with output size.
package span

// Entry is one correspondence. Del is a deleted original length; identity runs
// have Len > 0 and Del == 0. Zero-length identity entries anchor the output end.
type Entry struct {
	Orig int
	Out  int
	Len  int
	Del  int
}

// Builder appends entries left to right in both coordinate systems.
type Builder struct {
	e []Entry
}

func NewBuilder() *Builder { return &Builder{} }

// Identity records a kept run (a synthesized byte with identical coordinates is
// also identity).
func (b *Builder) Identity(orig, out, length int) {
	if length == 0 {
		return
	}
	if n := len(b.e); n > 0 {
		p := &b.e[n-1]
		if p.Del == 0 && p.Orig+p.Len == orig && p.Out+p.Len == out {
			p.Len += length
			return
		}
	}
	b.e = append(b.e, Entry{Orig: orig, Out: out, Len: length})
}

// Deleted records a removed original run mapped to output point outPos.
func (b *Builder) Deleted(orig, outPos, delLength int) {
	if delLength == 0 {
		return
	}
	b.e = append(b.e, Entry{Orig: orig, Out: outPos, Del: delLength})
}

// Entries returns the table.
func (b *Builder) Entries() []Entry { return b.e }

// PopLastIdentity removes one output byte from the trailing identity entry of
// es (splitting it) and appends a delete entry mapping that byte to newOut.
func PopLastIdentity(es []Entry, newOut int) []Entry {
	last := es[len(es)-1]
	nlOrig := last.Orig + last.Len - 1
	es = es[:len(es)-1]
	if last.Len > 1 {
		last.Len--
		es = append(es, last)
		nlOrig = last.Orig + last.Len
	}
	return append(es, Entry{Orig: nlOrig, Out: newOut, Del: 1})
}

// From replays entries onto a fresh builder.
func From(es []Entry) *Builder {
	b := NewBuilder()
	for _, x := range es {
		if x.Del > 0 {
			b.Deleted(x.Orig, x.Out, x.Del)
		} else {
			b.Identity(x.Orig, x.Out, x.Len)
		}
	}
	return b
}

// Map is an immutable correspondence answering bidirectional point queries.
type Map struct {
	e       []Entry
	origLen int
	outLen  int
	checked int
}

// NewMap builds from entries with the two domain end points.
func NewMap(e []Entry, origLen, outLen int) *Map {
	cp := make([]Entry, len(e))
	copy(cp, e)
	return &Map{e: cp, origLen: origLen, outLen: outLen}
}

func (m *Map) OrigLen() int     { return m.origLen }
func (m *Map) OutLen() int      { return m.outLen }
func (m *Map) Entries() []Entry { return m.e }
func (m *Map) Segments() int    { return len(m.e) }

// LastChecked reports the number of entries inspected by the last query.
func (m *Map) LastChecked() int { return m.checked }

// ToOrig maps an output point o in [0,OutLen] to an original point.
func (m *Map) ToOrig(o int) int {
	m.checked = 0
	lo, hi := 0, len(m.e)
	for lo < hi {
		m.checked++
		mid := int(uint(lo+hi) >> 1)
		e := m.e[mid]
		if e.Del == 0 && e.Out <= o && e.Out+e.Len > o {
			return e.Orig + (o - e.Out)
		}
		if (e.Del == 0 && e.Out+e.Len <= o) || (e.Del > 0 && e.Out <= o) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if o == m.outLen {
		return m.origLen
	}
	return m.origLen
}

// ToOut maps an original point i in [0,OrigLen] to an output point. Points
// inside a deleted run map to the run's output position (start of the next
// run, or the output end).
func (m *Map) ToOut(i int) int {
	m.checked = 0
	lo, hi := 0, len(m.e)
	for lo < hi {
		m.checked++
		mid := int(uint(lo+hi) >> 1)
		e := m.e[mid]
		end := e.Orig + e.Len
		if e.Del > 0 {
			end = e.Orig + e.Del
		}
		if i >= end {
			lo = mid + 1
			continue
		}
		if i < e.Orig {
			hi = mid
			continue
		}
		if e.Len > 0 && i >= e.Orig {
			return e.Out + (i - e.Orig)
		}
		return e.Out
	}
	if i == m.origLen {
		return m.outLen
	}
	return m.outLen
}
