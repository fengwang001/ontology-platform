package syntax

import (
	"errors"

	"ontology/class"
	"ontology/runes"
)

var (
	ErrUnclosedClass = errors.New("unclosed character class")
	ErrReversedRange = errors.New("reversed character range")
	ErrTrailingSlash = errors.New("trailing backslash")
	ErrEmptyClass    = errors.New("empty character class")
)

// Error is a compile error with a byte offset in the pattern.
type Error struct {
	Kind   error
	Offset int
}

func (e *Error) Error() string { return e.Kind.Error() }
func (e *Error) Unwrap() error { return e.Kind }

type Kind uint8

const (
	Literal Kind = iota
	Any
	Star
	Class
)

// Atom is one matching atom within a segment.
type Atom struct {
	Kind  Kind
	Point runes.Point
	Class class.Class
}

// Segment is a pattern segment between unescaped slash bytes.
type Segment struct {
	Atoms []Atom
	Glob  bool
}

// Pattern is a compiled syntactic pattern.
type Pattern struct {
	Segments []Segment
	Raw      string
}

// Limits are optional compile resource limits. Zero means unlimited.
type Limits struct {
	MaxBytes    int
	MaxGlobSegs int
}

// Compile parses pattern without using regexp or path globbing.
func Compile(pattern string, limits Limits) (*Pattern, error) {
	if limits.MaxBytes > 0 && len(pattern) > limits.MaxBytes {
		return nil, &Error{Kind: errors.New("pattern exceeds byte limit"), Offset: limits.MaxBytes}
	}
	p := &Pattern{Raw: pattern, Segments: []Segment{{}}}
	globSegs := 0
	for pos := 0; pos < len(pattern); {
		switch pattern[pos] {
		case '/':
			p.Segments = append(p.Segments, Segment{})
			pos++
		case '?':
			p.Segments[len(p.Segments)-1].Atoms = append(p.Segments[len(p.Segments)-1].Atoms, Atom{Kind: Any})
			pos++
		case '*':
			for pos < len(pattern) && pattern[pos] == '*' {
				pos++
			}
			p.Segments[len(p.Segments)-1].Atoms = append(p.Segments[len(p.Segments)-1].Atoms, Atom{Kind: Star})
		case '[':
			c, next, err := class.Parse(pattern, pos)
			if err != nil {
				return nil, mapError(err)
			}
			p.Segments[len(p.Segments)-1].Atoms = append(p.Segments[len(p.Segments)-1].Atoms, Atom{Kind: Class, Class: c})
			pos = next
		case '\\':
			if pos+1 >= len(pattern) {
				return nil, &Error{Kind: ErrTrailingSlash, Offset: pos}
			}
			point, size := runes.Decode(pattern, pos+1)
			if point.Rune == '/' {
				p.Segments = append(p.Segments, Segment{})
			} else {
				seg := &p.Segments[len(p.Segments)-1]
				seg.Atoms = append(seg.Atoms, Atom{Kind: Literal, Point: point})
			}
			pos += 1 + size
		default:
			point, size := runes.Decode(pattern, pos)
			p.Segments[len(p.Segments)-1].Atoms = append(p.Segments[len(p.Segments)-1].Atoms, Atom{Kind: Literal, Point: point})
			pos += size
		}
	}
	for i := range p.Segments {
		markGlob(&p.Segments[i])
		if p.Segments[i].Glob {
			globSegs++
		}
	}
	if limits.MaxGlobSegs > 0 && globSegs > limits.MaxGlobSegs {
		return nil, &Error{Kind: errors.New("too many globstar segments"), Offset: 0}
	}
	return p, nil
}

func markGlob(s *Segment) {
	if len(s.Atoms) == 0 {
		return
	}
	for _, a := range s.Atoms {
		if a.Kind != Star {
			return
		}
	}
	s.Glob = true
}

func mapError(err error) error {
	var ce *class.Error
	if errors.As(err, &ce) {
		kind := ErrUnclosedClass
		switch {
		case errors.Is(ce, class.ErrEmpty):
			kind = ErrEmptyClass
		case errors.Is(ce, class.ErrRange):
			kind = ErrReversedRange
		}
		return &Error{Kind: kind, Offset: ce.Offset}
	}
	return err
}
