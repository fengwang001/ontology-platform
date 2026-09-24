// Package span records correspondences between original byte intervals and
// normalized output byte intervals and answers both mapping directions by
// binary search. It has no package dependencies.
//
// Three entry kinds are supported:
//   direct   orig==out>0: 1:1 mapping [origLo,origLo+n) <-> [outLo,outLo+n)
//   deletion out==0:      removed original bytes [origLo,origHi) at output point outLo
//   insertion orig==0:    an emitted byte with no original source at anchor origLo
package span

// Entry is one mapping interval. Coordinates are half-open.
type Entry struct {
	OrigLo, OrigHi int
	OutLo, OutHi   int
}

// Map is an immutable ordered set of entries covering a whole stream.
type Map struct {
	es     []Entry
	olen   int // output length
	probes int // entries inspected by the most recent query
}

// Build constructs a Map from ordered entries and the output length.
func Build(es []Entry, outLen int) *Map { return &Map{es: es, olen: outLen} }

// Entries exposes the underlying entries (used when stitching segment maps).
func (m *Map) Entries() []Entry { return m.es }

// OrigLen is the original length; OutLen is the output length.
func (m *Map) OrigLen() int {
	if len(m.es) == 0 {
		return 0
	}
	return m.es[len(m.es)-1].OrigHi
}
func (m *Map) OutLen() int { return m.olen }

// Probes reports how many entries the most recent ToOrig/ToOut query
// inspected (including binary-search steps); it is intentionally unexported
// in spirit, exposed read-only for tests/diagnostics.
func (m *Map) Probes() int { return m.probes }

func (m *Map) searchOrig(i int) Entry {
	lo, hi, n := 0, len(m.es), 0
	for lo < hi {
		n++
		mid := (lo + hi) / 2
		if m.es[mid].OrigHi <= i {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	n++
	m.probes = n
	if lo >= len(m.es) {
		return m.es[len(m.es)-1]
	}
	if m.es[lo].OrigLo <= i || lo == 0 {
		return m.es[lo]
	}
	return m.es[lo-1]
}

// ToOut maps an original offset i in [0,OrigLen] to an output offset.
func (m *Map) ToOut(i int) int {
	e := m.searchOrig(i)
	if e.OrigHi-e.OrigLo == 0 || i < e.OrigLo {
		return e.OutLo // deletion run / boundary point
	}
	return e.OutLo + (i - e.OrigLo)
}

func (m *Map) searchOut(o int) Entry {
	lo, hi, n := 0, len(m.es), 0
	for lo < hi {
		n++
		mid := (lo + hi) / 2
		if m.es[mid].OutHi <= o {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	n++
	m.probes = n
	if lo >= len(m.es) {
		return m.es[len(m.es)-1]
	}
	if m.es[lo].OutLo <= o || lo == 0 {
		return m.es[lo]
	}
	return m.es[lo-1]
}

// ToOrig maps an output offset o in [0,OutLen] to an original offset.
func (m *Map) ToOrig(o int) int {
	e := m.searchOut(o)
	if e.OutHi-e.OutLo == 0 || o < e.OutLo {
		return e.OrigLo // deletion point / insertion anchor
	}
	return e.OrigLo + (o - e.OutLo)
}

// Builder accumulates entries in coordinate order, merging adjacent direct
// runs so the entry count grows only with deletion points.
type Builder struct {
	es    []Entry
	olen  int
	origN int
}

func (b *Builder) last() *Entry {
	if len(b.es) == 0 {
		return nil
	}
	return &b.es[len(b.es)-1]
}

// Direct records n bytes that pass through unchanged.
func (b *Builder) Direct(n int) {
	if n <= 0 {
		return
	}
	if e := b.last(); e != nil && e.OutHi-e.OutLo == e.OrigHi-e.OrigLo &&
		e.OutHi-e.OutLo > 0 && e.OrigHi == b.origN && e.OutHi == b.olen {
		e.OrigHi += n
		e.OutHi += n
	} else {
		b.es = append(b.es, Entry{b.origN, b.origN + n, b.olen, b.olen + n})
	}
	b.origN += n
	b.olen += n
}

// Delete records n removed original bytes at the current output point.
func (b *Builder) Delete(n int) {
	if n <= 0 {
		return
	}
	if e := b.last(); e != nil && e.OutLo == b.olen && e.OutHi == b.olen && e.OrigHi == b.origN {
		e.OrigHi += n
	} else {
		b.es = append(b.es, Entry{b.origN, b.origN + n, b.olen, b.olen})
	}
	b.origN += n
}

// Insert records one emitted byte with no original source at anchor orig.
func (b *Builder) Insert(orig int) {
	b.es = append(b.es, Entry{orig, orig, b.olen, b.olen + 1})
	b.olen++
}

// Map freezes the accumulated entries.
func (b *Builder) Map() *Map { return Build(b.es, b.olen) }

// Entries exposes the accumulated entries in order.
func (b *Builder) Entries() []Entry { return b.es }

// Use replaces the builder state with pre-built ordered entries whose
// coordinates are already absolute (used when stitching/trimming maps).
func (b *Builder) Use(es []Entry, outLen int) {
	b.es = append([]Entry(nil), es...)
	b.olen = outLen
	b.origN = 0
	if len(es) > 0 {
		b.origN = es[len(es)-1].OrigHi
	}
}
