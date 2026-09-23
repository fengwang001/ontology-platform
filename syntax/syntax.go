// Package syntax compiles a pattern into segments and atoms.
package syntax

import (
	"errors"
	"strings"

	"ontology/class"
	"ontology/runes"
)

// AtomKind is the kind of a compiled atom.
type AtomKind int

const (
	Lit  AtomKind = iota // literal bytes
	Any                  // ?
	Star                 // *
	Cls                  // [...]
)

// Atom is one compiled unit inside a segment.
type Atom struct {
	Kind AtomKind
	Raw  string // literal bytes for Lit
	Cls  class.Class
}

// Seg is one path segment of a pattern. Double means a bare "**".
type Seg struct {
	Double bool
	Atoms  []Atom
}

// Pattern is a compiled pattern.
type Pattern struct {
	Raw   string
	Segs  []Seg
	NAtom int // atoms plus Double segments
}

// Limits bounds pattern compilation; zero values use defaults.
type Limits struct {
	MaxBytes  int
	MaxDouble int
}

var (
	ErrTooLong       = errors.New("pattern exceeds byte limit")
	ErrTooManyDouble = errors.New("pattern exceeds ** segment limit")
)

func (l Limits) withDefaults() Limits {
	if l.MaxBytes <= 0 {
		l.MaxBytes = 4096
	}
	if l.MaxDouble <= 0 {
		l.MaxDouble = 16
	}
	return l
}

// Compile compiles pat. On error no usable Pattern is returned.
func Compile(pat string, lim Limits) (*Pattern, error) {
	lim = lim.withDefaults()
	if len(pat) > lim.MaxBytes {
		return nil, ErrTooLong
	}
	p := &Pattern{Raw: pat}
	ndouble, off := 0, 0
	for _, s := range strings.Split(pat, "/") {
		if s == "**" {
			ndouble++
			if ndouble > lim.MaxDouble {
				return nil, ErrTooManyDouble
			}
			p.Segs = append(p.Segs, Seg{Double: true})
			p.NAtom++
		} else {
			seg, err := compileSeg(s, off)
			if err != nil {
				return nil, err
			}
			p.Segs = append(p.Segs, seg)
			p.NAtom += len(seg.Atoms)
		}
		off += len(s) + 1
	}
	return p, nil
}

func compileSeg(s string, base int) (Seg, error) {
	var seg Seg
	for i := 0; i < len(s); {
		switch s[i] {
		case '?':
			seg.Atoms = append(seg.Atoms, Atom{Kind: Any})
			i++
		case '*':
			seg.Atoms = append(seg.Atoms, Atom{Kind: Star})
			i++
		case '[':
			c, n, err := class.Parse(s, i)
			if err != nil {
				if e, ok := err.(*class.Error); ok {
					e.Offset += base
				}
				return seg, err
			}
			seg.Atoms = append(seg.Atoms, Atom{Kind: Cls, Cls: c})
			i = n
		case '\\':
			if i+1 >= len(s) {
				return seg, &class.Error{Kind: class.KindBackslash, Offset: base + i}
			}
			_, raw, n := runes.Step(s, i+1)
			seg.Atoms = append(seg.Atoms, Atom{Kind: Lit, Raw: raw})
			i = n
		default:
			_, raw, n := runes.Step(s, i)
			seg.Atoms = append(seg.Atoms, Atom{Kind: Lit, Raw: raw})
			i = n
		}
	}
	return seg, nil
}
