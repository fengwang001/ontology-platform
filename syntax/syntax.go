// Package syntax compiles a pattern into segments of atoms and reports
// decidable syntax errors carrying byte offsets into the pattern.
package syntax

import (
	"errors"
	"strings"

	"ontology/class"
	"ontology/runes"
)

var (
	ErrUnterminatedClass = class.ErrUnterminated
	ErrEmptyClass        = class.ErrEmpty
	ErrReversedRange     = class.ErrReversed
	ErrTrailingBackslash = errors.New("pattern or segment ends with a lone '\\'")
	ErrTooLong           = errors.New("pattern exceeds byte limit")
	ErrTooManyGlobstars  = errors.New("pattern exceeds ** segment limit")
)

// Error is a compile failure with a byte offset into the pattern.
type Error = class.Error

// Limits bound pattern size; zero fields fall back to defaults.
type Limits struct {
	MaxBytes     int
	MaxGlobstars int
}

const (
	DefaultMaxBytes     = 4096
	DefaultMaxGlobstars = 16
)

// AtomKind identifies the matching behaviour of one Atom.
type AtomKind uint8

const (
	Lit  AtomKind = iota // literal bytes
	Any                  // ?
	Star                 // *
	Cls                  // [...]
)

// Atom is one matching unit inside a segment.
type Atom struct {
	Kind AtomKind
	Lit  string // raw bytes for Lit (may hold invalid UTF-8)
	Cl   *class.Class
}

// Segment is one path segment of a compiled pattern.
type Segment struct {
	Glob  bool // the whole segment is exactly "**"
	Atoms []Atom
}

// Pattern is a compiled pattern.
type Pattern struct {
	Raw  string
	Segs []Segment
}

// Compile parses pat. On failure the returned Pattern is nil.
func Compile(pat string, lim Limits) (*Pattern, error) {
	if lim.MaxBytes <= 0 {
		lim.MaxBytes = DefaultMaxBytes
	}
	if lim.MaxGlobstars <= 0 {
		lim.MaxGlobstars = DefaultMaxGlobstars
	}
	if len(pat) > lim.MaxBytes {
		return nil, &Error{Kind: ErrTooLong, Offset: lim.MaxBytes}
	}
	parts := strings.Split(pat, "/")
	p := &Pattern{Raw: pat, Segs: make([]Segment, len(parts))}
	base, globs := 0, 0
	for i, s := range parts {
		if s == "**" {
			globs++
			if globs > lim.MaxGlobstars {
				return nil, &Error{Kind: ErrTooManyGlobstars, Offset: base}
			}
			p.Segs[i].Glob = true
		} else {
			atoms, err := parseAtoms(s, base)
			if err != nil {
				return nil, err
			}
			p.Segs[i].Atoms = atoms
		}
		base += len(s) + 1
	}
	return p, nil
}

func parseAtoms(seg string, base int) ([]Atom, error) {
	var atoms []Atom
	star := false // collapse runs of '*'
	for i := 0; i < len(seg); {
		switch seg[i] {
		case '*':
			if !star {
				atoms = append(atoms, Atom{Kind: Star})
			}
			star = true
			i++
		case '?':
			atoms = append(atoms, Atom{Kind: Any})
			star = false
			i++
		case '[':
			cl, next, err := class.Parse([]byte(seg), i)
			if err != nil {
				if e, ok := err.(*Error); ok {
					e.Offset += base
				}
				return nil, err
			}
			atoms = append(atoms, Atom{Kind: Cls, Cl: cl})
			star = false
			i = next
		case '\\':
			if i+1 >= len(seg) {
				return nil, &Error{Kind: ErrTrailingBackslash, Offset: base + i}
			}
			_, size := runes.Decode([]byte(seg), i+1)
			atoms = append(atoms, Atom{Kind: Lit, Lit: seg[i+1 : i+1+size]})
			star = false
			i += 1 + size
		default:
			_, size := runes.Decode([]byte(seg), i)
			atoms = append(atoms, Atom{Kind: Lit, Lit: seg[i : i+size]})
			star = false
			i += size
		}
	}
	return atoms, nil
}
