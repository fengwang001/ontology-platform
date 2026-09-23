package class

import (
	"errors"
	"unicode/utf8"

	"ontology/runes"
)

var (
	ErrUnclosed = errors.New("character class is not closed")
	ErrEmpty    = errors.New("character class is empty")
	ErrRange    = errors.New("character class range is reversed")
)

// Error locates a character-class compile failure at a byte offset.
type Error struct {
	Kind   error
	Offset int
}

func (e *Error) Error() string { return e.Kind.Error() }
func (e *Error) Unwrap() error { return e.Kind }

type term struct {
	p     runes.Point
	valid bool
}

// Class is a compiled positive or negated character class.
type Class struct {
	negated bool
	terms   []term
}

// Parse consumes a class beginning at the opening '[' and returns it with the
// offset immediately after the closing ']'.
func Parse(pattern string, start int) (Class, int, error) {
	pos := start + 1
	c := Class{}
	if pos < len(pattern) && (pattern[pos] == '!' || pattern[pos] == '^') {
		c.negated = true
		pos++
	}
	first := true
	for pos >= len(pattern) || pattern[pos] != ']' || first {
		if pos >= len(pattern) {
			if first {
				return Class{}, pos, &Error{Kind: ErrEmpty, Offset: pos}
			}
			return Class{}, pos, &Error{Kind: ErrUnclosed, Offset: start}
		}
		t, next, err := readTerm(pattern, pos)
		if err != nil {
			return Class{}, pos, err
		}
		if next < len(pattern) && pattern[next] == '-' && next+1 < len(pattern) && pattern[next+1] != ']' {
			end, after, endErr := readTerm(pattern, next+1)
			if endErr != nil {
				return Class{}, next + 1, endErr
			}
			if !t.valid || !end.valid || t.p.Rune > end.p.Rune {
				return Class{}, pos, &Error{Kind: ErrRange, Offset: pos}
			}
			for r := t.p.Rune; r <= end.p.Rune; r++ {
				c.terms = append(c.terms, term{p: runes.Point{Rune: r, Size: utf8.RuneLen(r)}, valid: true})
			}
			pos = after
		} else {
			c.terms = append(c.terms, t)
			pos = next
		}
		first = false
	}
	return c, pos + 1, nil
}

func readTerm(pattern string, pos int) (term, int, error) {
	if pattern[pos] == '\\' {
		if pos+1 >= len(pattern) {
			return term{}, pos, &Error{Kind: ErrUnclosed, Offset: pos}
		}
		p, size := runes.Decode(pattern, pos+1)
		return term{p: p, valid: p.Rune != utf8.RuneError || p.Size != 1}, pos + 1 + size, nil
	}
	p, size := runes.Decode(pattern, pos)
	return term{p: p, valid: p.Rune != utf8.RuneError || p.Size != 1}, pos + size, nil
}

// Match reports whether one logical code point belongs to the class.
func (c Class) Match(p runes.Point) bool {
	found := false
	for _, t := range c.terms {
		if !t.valid {
			if p.Rune == utf8.RuneError && p.Size == 1 && p.Byte == t.p.Byte {
				found = true
			}
			continue
		}
		if p.Rune == t.p.Rune && !(p.Rune == utf8.RuneError && p.Size == 1) {
			found = true
		}
	}
	if p.Rune == '/' {
		return c.negated == false && found
	}
	return found != c.negated
}
