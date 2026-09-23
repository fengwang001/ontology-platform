// Package syntax compiles wildcard patterns into segments and atoms.
package syntax

import (
	"ontology/class"
	"ontology/runes"
)

// Atom kinds.
const (
	ALiteral = 1
	AAny     = 2
	AStar    = 3
	AClass   = 4
)

// Atom is one within-segment matching unit.
type Atom struct {
	Kind int
	Lit  runes.Rune
	Cls  *class.Class
}

// Segment is either a cross-segment double-star or a plain atom list.
type Segment struct {
	DoubleStar bool
	Atoms      []Atom
}

// Pattern is a compiled immutable pattern.
type Pattern struct {
	Raw       string
	Segments  []Segment
	AtomCount int
}

// Limits cap compilation; zero fields get defaults.
type Limits struct {
	MaxBytes    int
	MaxStarStar int
}

// Default limits.
var DefaultLimits = Limits{MaxBytes: 1 << 20, MaxStarStar: 64}

func (l Limits) orDefault() Limits {
	d := DefaultLimits
	if l.MaxBytes > 0 {
		d.MaxBytes = l.MaxBytes
	}
	if l.MaxStarStar > 0 {
		d.MaxStarStar = l.MaxStarStar
	}
	return d
}

// Compile parses pattern.
func Compile(pattern string, lim Limits) (*Pattern, error) {
	lim = lim.orDefault()
	if len(pattern) > lim.MaxBytes {
		return nil, &Error{Kind: ErrTooLong, Offset: lim.MaxBytes}
	}
	p := &Pattern{Raw: pattern}
	if pattern == "" {
		return p, nil
	}
	cur := runes.New(pattern)
	for {
		var atoms []Atom
		stars := 0
	segmentLoop:
		for {
			r, ok := cur.Next()
			if !ok {
				break segmentLoop
			}
			switch {
			case r.R == '/':
				break segmentLoop
			case r.R == '?':
				atoms = append(atoms, Atom{Kind: AAny})
			case r.R == '*':
				atoms = append(atoms, Atom{Kind: AStar})
				stars++
			case r.R == '[':
				cls, err := class.Parse(cur)
				if err != nil {
					return nil, wrapClass(err)
				}
				atoms = append(atoms, Atom{Kind: AClass, Cls: cls})
			case r.R == '\\':
				e, ok2 := cur.Next()
				if !ok2 {
					return nil, &Error{Kind: ErrTrailingBackslash, Offset: cur.Pos() - 1}
				}
				atoms = append(atoms, Atom{Kind: ALiteral, Lit: e})
			default:
				atoms = append(atoms, Atom{Kind: ALiteral, Lit: r})
			}
		}
		if len(atoms) == 2 && stars == 2 {
			if len(p.Segments) >= lim.MaxStarStar {
				return nil, &Error{Kind: ErrTooManyStars, Offset: cur.Pos()}
			}
			p.Segments = append(p.Segments, Segment{DoubleStar: true})
		} else {
			p.Segments = append(p.Segments, Segment{Atoms: atoms})
		}
		p.AtomCount += len(atoms)
		if cur.Done() {
			return p, nil
		}
	}
}

func wrapClass(err error) error {
	ce := err.(*class.Error)
	kind := map[error]error{
		class.ErrUnclosed:       ErrUnclosedClass,
		class.ErrEmptyClass:     ErrEmptyClass,
		class.ErrReversedRange:  ErrReversedRange,
		class.ErrTrailingEscape: ErrTrailingBackslash,
	}[ce.Kind]
	return &Error{Kind: kind, Offset: ce.Offset}
}

// Sentinel error kinds.
var (
	ErrUnclosedClass     = synErr("unclosed character class")
	ErrEmptyClass        = synErr("empty character class")
	ErrReversedRange     = synErr("reversed character range")
	ErrTrailingBackslash = synErr("trailing backslash")
	ErrTooLong           = synErr("pattern too long")
	ErrTooManyStars      = synErr("too many double-star segments")
)

// Error is a syntax error with the byte offset in the pattern.
type Error struct {
	Kind   error
	Offset int
}

func (e *Error) Error() string { return e.Kind.Error() }
func (e *Error) Unwrap() error { return e.Kind }

type synErr string

func (e synErr) Error() string { return string(e) }
