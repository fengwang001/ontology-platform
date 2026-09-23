// Package class parses and matches glob character classes such as [a-z!].
// It depends only on runes.
package class

import (
	"strings"

	"ontology/runes"
)

// Error kinds, distinguishable via Errors.Is.
const (
	ErrUnclosed = "class: unclosed '['"
	ErrReversed = "class: reversed range"
)

// Error is a decidable class syntax error carrying a byte offset.
type Error struct {
	Kind   string
	Offset int
}

func (e *Error) Error() string { return e.Kind }

// Class is one parsed [...] atom. Negated classes never match '/'.
type Class struct {
	Negate bool
	lits   []runes.Rune
	lo     []runes.Rune
	hi     []runes.Rune
}

// Match reports whether r belongs to the class.
func (c *Class) Match(r runes.Rune) bool {
	if r.R == '/' && !r.IsBad() {
		return false
	}
	hit := false
	for _, l := range c.lits {
		if l.Equal(r) {
			hit = true
		}
	}
	for i := range c.lo {
		if r.Cmp(c.lo[i]) >= 0 && r.Cmp(c.hi[i]) <= 0 {
			hit = true
		}
	}
	return hit != c.Negate
}

// Parse consumes one class beginning at s[off] == '['. It returns the class,
// the offset just past the closing ']', or an *Error.
func Parse(s string, off int) (*Class, int, error) {
	c := &Class{}
	i := off + 1
	if i < len(s) && (s[i] == '!' || s[i] == '^') {
		c.Negate = true
		i++
	}
	first := true
	for {
		if i >= len(s) {
			return nil, 0, &Error{Kind: ErrUnclosed, Offset: off}
		}
		if s[i] == ']' && (!first || strings.IndexByte(s[i+1:], ']') >= 0) {
			if len(c.lits) == 0 && len(c.lo) == 0 {
				return nil, 0, &Error{Kind: "class: empty class", Offset: off}
			}
			return c, i + 1, nil
		}
		if s[i] == ']' && first {
			return nil, 0, &Error{Kind: "class: empty class", Offset: off}
		}
		first = false
		r, size := runes.First(s[i:])
		if r.R == '\\' {
			if i+1 >= len(s) {
				return nil, 0, &Error{Kind: ErrUnclosed, Offset: off}
			}
			i++
			r, size = runes.First(s[i:])
		}
		i += size
		if i < len(s) && s[i] == '-' && i+1 < len(s) && s[i+1] != ']' {
			i++
			hi, hsize := runes.First(s[i:])
			if hi.R == '\\' {
				if i+1 >= len(s) {
					return nil, 0, &Error{Kind: ErrUnclosed, Offset: off}
				}
				i++
				hi, hsize = runes.First(s[i:])
			}
			if r.Cmp(hi) > 0 {
				return nil, 0, &Error{Kind: ErrReversed, Offset: i}
			}
			c.lo, c.hi = append(c.lo, r), append(c.hi, hi)
			i += hsize
		} else {
			c.lits = append(c.lits, r)
		}
	}
}
