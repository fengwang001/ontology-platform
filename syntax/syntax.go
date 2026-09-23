// Package syntax compiles a wildcard pattern into segments of atoms.
package syntax

import (
	"errors"

	"ontology/class"
	"ontology/runes"
)

const (
	ALiteral = iota
	AQuestion
	AStar
	AClass
)

// Decidable sentinel errors (the four distinguishable syntax failures plus
// resource limits).
var (
	ErrUnterminatedClass = errors.New("syntax: unterminated class")
	ErrReversedRange     = errors.New("syntax: reversed range")
	ErrTrailingSlashEsc  = errors.New("syntax: trailing backslash")
	ErrEmptyClass        = errors.New("syntax: empty class")
	ErrPatternTooLong    = errors.New("syntax: pattern too long")
	ErrTooManyDoubleStar = errors.New("syntax: too many ** segments")
)

// Error carries the byte offset in the pattern and its kind.
type Error struct {
	Offset int
	Kind   error
}

func (e *Error) Error() string { return e.Kind.Error() }
func (e *Error) Unwrap() error { return e.Kind }

// Atom is one within-segment matcher descriptor.
type Atom struct {
	Kind  int
	Lit   runes.Item
	Class *class.Class
}

// Segment is a slash-delimited piece of a pattern.
type Segment struct {
	DoubleStar bool // whole segment is exactly "**"
	Atoms      []Atom
}

// Pattern is a compiled pattern.
type Pattern struct {
	Raw       string
	Segments  []Segment
	AtomCount int
}

// Limits bounds compilation resources. Zero fields use DefaultLimits.
type Limits struct{ MaxBytes, MaxDoubleStars int }

// DefaultLimits are generous defaults.
var DefaultLimits = Limits{MaxBytes: 1 << 20, MaxDoubleStars: 1024}

// Compile parses pattern into segments, enforcing lim.
func Compile(pattern string, lim Limits) (*Pattern, error) {
	if lim.MaxBytes == 0 {
		lim.MaxBytes = DefaultLimits.MaxBytes
	}
	if lim.MaxDoubleStars == 0 {
		lim.MaxDoubleStars = DefaultLimits.MaxDoubleStars
	}
	if len(pattern) > lim.MaxBytes {
		return nil, &Error{Offset: len(pattern), Kind: ErrPatternTooLong}
	}
	p := &Pattern{Raw: pattern}
	seg, segStart, esc := Segment{}, 0, false
	flush := func(end int) {
		if !esc && pattern[segStart:end] == "**" && len(seg.Atoms) == 1 {
			seg.DoubleStar = true
		}
		p.Segments, seg, esc = append(p.Segments, seg), Segment{}, false
	}
	for i := 0; i < len(pattern); {
		switch pattern[i] {
		case '/':
			flush(i)
			segStart, i = i+1, i+1
		case '?':
			seg.Atoms, p.AtomCount = append(seg.Atoms, Atom{Kind: AQuestion}), p.AtomCount+1
			i++
		case '*':
			for i < len(pattern) && pattern[i] == '*' {
				i++
			}
			seg.Atoms, p.AtomCount = append(seg.Atoms, Atom{Kind: AStar}), p.AtomCount+1
		case '[':
			cl, next, err := class.Parse(pattern, i)
			if err != nil {
				return nil, wrapClass(err.(*class.Error))
			}
			seg.Atoms = append(seg.Atoms, Atom{Kind: AClass, Class: cl})
			p.AtomCount, i = p.AtomCount+1, next
		case '\\':
			if i+1 >= len(pattern) {
				return nil, &Error{Offset: i, Kind: ErrTrailingSlashEsc}
			}
			esc = true
			it := runes.Items(pattern[i+1:])[0]
			seg.Atoms = append(seg.Atoms, Atom{Kind: ALiteral, Lit: it})
			p.AtomCount, i = p.AtomCount+1, i+1+it.Bits
		default:
			it := runes.Items(pattern[i:])[0]
			seg.Atoms = append(seg.Atoms, Atom{Kind: ALiteral, Lit: it})
			p.AtomCount, i = p.AtomCount+1, i+it.Bits
		}
	}
	flush(len(pattern))
	dstars := 0
	for _, s := range p.Segments {
		if s.DoubleStar {
			dstars++
		}
	}
	if dstars > lim.MaxDoubleStars {
		return nil, &Error{Offset: 0, Kind: ErrTooManyDoubleStar}
	}
	return p, nil
}

func wrapClass(e *class.Error) *Error {
	k := ErrEmptyClass
	switch {
	case errors.Is(e, class.ErrUnterminated):
		k = ErrUnterminatedClass
	case errors.Is(e, class.ErrReversed):
		k = ErrReversedRange
	}
	return &Error{Offset: e.Offset, Kind: k}
}
