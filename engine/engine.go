package engine

import (
	"math/bits"

	"ontology/runes"
	"ontology/syntax"
)

type nodeKind uint8

const (
	nBoundary nodeKind = iota
	nAtom
)

type node struct {
	kind   nodeKind
	atom   syntax.Atom
	eps    []uint
	accept bool
	glob   bool
	start  bool
	lead   bool
}

type Matcher struct {
	nodes []node
	steps uint64
}

func Compile(p *syntax.Pattern) *Matcher {
	m := &Matcher{nodes: []node{{kind: nBoundary}}}
	cur := uint(0)
	for si, seg := range p.Segments {
		if seg.Glob {
			star := m.atom(syntax.Atom{Kind: syntax.Star})
			mid := star + 1
			slash := m.atom(syntax.Atom{Kind: syntax.Literal, Point: runes.Point{Rune: '/'}})
			cont := slash + 1
			m.edge(cur, star)
			m.edge(star, star)
			m.edge(cur, slash)
			m.edge(cont, star)
			m.edge(cur, mid)
			m.edge(cont, mid)
			if si+1 < len(p.Segments) {
				suffix := m.atom(syntax.Atom{Kind: syntax.Literal, Point: runes.Point{Rune: '/'}})
				next := suffix + 1
				m.edge(cur, next)
				m.edge(cur, suffix)
				m.edge(mid, suffix)
				m.edge(cont, suffix)
				cur = next
			} else {
				accept := m.boundary()
				m.edge(cur, accept)
				m.edge(mid, accept)
				m.edge(cont, accept)
				cur = accept
			}
			continue
		}
		for _, atom := range seg.Atoms {
			id := m.atom(atom)
			m.edge(cur, id)
			if atom.Kind == syntax.Star {
				m.edge(id, id)
				m.edge(cur, id+1)
			}
			cur = id + 1
			m.ensureBoundary(cur)
		}
		if si+1 < len(p.Segments) {
			id := m.atom(syntax.Atom{Kind: syntax.Literal, Point: runes.Point{Rune: '/'}})
			m.edge(cur, id)
			cur = id + 1
			m.ensureBoundary(cur)
		}
	}
	m.nodes[cur].accept = true
	return m
}

func (m *Matcher) atom(a syntax.Atom) uint {
	id := uint(len(m.nodes))
	m.nodes = append(m.nodes, node{kind: nAtom, atom: a}, node{kind: nBoundary})
	return id
}

func (m *Matcher) boundary() uint {
	id := uint(len(m.nodes))
	m.nodes = append(m.nodes, node{kind: nBoundary})
	return id
}

func (m *Matcher) ensureBoundary(id uint) {
	for uint(len(m.nodes)) <= id {
		m.nodes = append(m.nodes, node{kind: nBoundary})
	}
}

func (m *Matcher) edge(from, to uint) { m.nodes[from].eps = append(m.nodes[from].eps, to) }

func (m *Matcher) Match(path string) bool {
	m.steps = 0
	states := m.closure(bitset{1}, 0)
	for offset := 0; offset < len(path); {
		p, size := runes.Decode(path, offset)
		next := bitset{}
		for wi, word := range states {
			for word != 0 {
				bit := bits.TrailingZeros64(word)
				word &^= 1 << uint(bit)
				id := uint(wi*64 + bit)
				if id < uint(len(m.nodes)) && m.consumes(id, p, offset) {
					next.set(id + 1)
				}
			}
		}
		offset += size
		states = m.closure(next, offset)
	}
	return states.has(curAccept(&m.nodes))
}

func curAccept(nodes *[]node) uint {
	for i := len(*nodes) - 1; i >= 0; i-- {
		if (*nodes)[i].accept {
			return uint(i)
		}
	}
	return 0
}

func (m *Matcher) closure(in bitset, offset int) bitset {
	out := make(bitset, (len(m.nodes)+63)/64)
	stack := make([]uint, 0, len(m.nodes))
	for _, w := range in {
		for w != 0 {
			b := bits.TrailingZeros64(w)
			w &^= 1 << uint(b)
			id := uint(b)
			if !out.has(id) {
				out.set(id)
				stack = append(stack, id)
			}
		}
	}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n := &m.nodes[id]
		m.steps++
		for _, to := range n.eps {
			m.steps++
			if !out.has(to) {
				out.set(to)
				stack = append(stack, to)
			}
		}
		if n.glob {
			m.steps++
		}
	}
	return out
}

func (m *Matcher) consumes(id uint, p runes.Point, offset int) bool {
	n := &m.nodes[id]
	if n.kind != nAtom {
		return false
	}
	m.steps++
	a := n.atom
	switch a.Kind {
	case syntax.Any:
		return p.Rune != '/'
	case syntax.Literal:
		return runes.Equal(a.Point, p)
	case syntax.Class:
		return a.Class.Match(p)
	case syntax.Star:
		if n.glob {
			return p.Rune != '/' || n.start && offset == 0
		}
		return p.Rune != '/'
	}
	return false
}

func (m *Matcher) Steps() uint64 { return m.steps }

type bitset []uint64

func (s bitset) has(id uint) bool {
	return int(id/64) < len(s) && s[id/64]&(1<<(id%64)) != 0
}

func (s *bitset) set(id uint) {
	if need := int(id/64) + 1; need > len(*s) {
		*s = append(*s, make(bitset, need-len(*s))...)
	}
	(*s)[id/64] |= 1 << (id % 64)
}
