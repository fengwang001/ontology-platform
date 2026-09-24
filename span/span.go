// Package span records original↔normalized offset correspondences as anchors.
package span

// Anchor (a, b) means original offset a maps to output offset b.
// Between anchors offsets move 1:1; an A-only gap is a deleted run,
// a B-only gap is an inserted run.
type Anchor struct{ A, B int }

// Builder incrementally constructs an offset map.
type Builder struct{ m Map }

// Map is an immutable correspondence table.
type Map struct {
	an     []Anchor
	probes int // intervals examined by the most recent directional query
}

// NewBuilder starts a map with the (0,0) origin anchor.
func NewBuilder() *Builder { return &Builder{Map{an: []Anchor{{}}}} }

func slope1(p, q Anchor) bool { return q.A-p.A == q.B-p.B }

// Keep records a run of n bytes present in both streams.
func (b *Builder) Keep(n int) {
	if n <= 0 {
		return
	}
	an := b.m.an
	last := an[len(an)-1]
	next := Anchor{last.A + n, last.B + n}
	if len(an) >= 2 && slope1(an[len(an)-2], last) {
		an[len(an)-1] = next
	} else {
		b.m.an = append(an, next)
	}
}

// Delete records n original bytes removed before the next kept byte.
func (b *Builder) Delete(n int) {
	if n <= 0 {
		return
	}
	l := b.m.an[len(b.m.an)-1]
	b.m.an = append(b.m.an, Anchor{l.A + n, l.B})
}

// Insert records n output bytes with no original source.
func (b *Builder) Insert(n int) {
	if n <= 0 {
		return
	}
	l := b.m.an[len(b.m.an)-1]
	b.m.an = append(b.m.an, Anchor{l.A, l.B + n})
}

// Truncate drops anchors beyond original offset a and sets the end to (a, b).
func (b *Builder) Truncate(a, bb int) {
	lo, hi := 0, len(b.m.an)
	for lo < hi {
		mid := (lo + hi) / 2
		if b.m.an[mid].A < a {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	b.m.an = b.m.an[:lo]
	if n := len(b.m.an); n > 0 && b.m.an[n-1].A == a {
		b.m.an[n-1].B = bb
	} else {
		b.m.an = append(b.m.an, Anchor{a, bb})
	}
}

func (b *Builder) Build() Map {
	m := b.m
	m.an = append([]Anchor(nil), m.an...)
	return m
}

func (m Map) Anchors() []Anchor { return m.an }

// Probes reports intervals examined by the most recent directional query.
func (m *Map) Probes() int { return m.probes }

func (m *Map) ToOrig(o int) int {
	an, n := m.an, len(m.an)
	lo, hi, probes := 0, n, 0
	for lo < hi {
		probes++
		mid := (lo + hi) / 2
		if an[mid].B <= o {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	prev := an[lo-1]
	r := prev.A + (o - prev.B)
	if lo < n && r > an[lo].A {
		r = an[lo].A
	}
	m.probes = probes
	return r
}

func (m *Map) ToOut(i int) int {
	an, n := m.an, len(m.an)
	lo, hi, probes := 0, n, 0
	for lo < hi {
		probes++
		mid := (lo + hi) / 2
		if an[mid].A <= i {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	prev := an[lo-1]
	r := prev.B + (i - prev.A)
	if lo < n && r > an[lo].B {
		r = an[lo].B
	}
	m.probes = probes
	return r
}

// Merge concatenates tables shifted by (da, db), coalescing slope-1 joins.
func Merge(parts []Map, shifts []Anchor) Map {
	var out []Anchor
	for p, m := range parts {
		for k, an := range m.an {
			if p > 0 && k == 0 {
				continue
			}
			out = append(out, Anchor{an.A + shifts[p].A, an.B + shifts[p].B})
		}
	}
	kept := out[:0]
	for k, an := range out {
		if k > 0 && k < len(out)-1 && slope1(out[k-1], an) && slope1(an, out[k+1]) {
			continue
		}
		kept = append(kept, an)
	}
	if len(kept) == 0 {
		kept = []Anchor{{}}
	}
	return Map{an: kept}
}
