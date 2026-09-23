// Package class parses and matches [...] character classes.
package class

import (
	"fmt"

	"ontology/runes"
)

// Kind identifies a distinguishable pattern syntax error.
type Kind int

const (
	KindUnclosed  Kind = iota // '[' never closed
	KindReversed              // range with lo > hi, e.g. [z-a]
	KindEmpty                 // empty class []
	KindBackslash             // pattern ends with a single '\'
)

// Error is a pattern syntax error carrying its byte Offset.
type Error struct {
	Kind   Kind
	Offset int
}

func (e *Error) Error() string {
	return fmt.Sprintf("pattern syntax error kind=%d at byte offset %d", e.Kind, e.Offset)
}

// Class is a compiled character class.
type Class struct {
	neg    bool
	ranges [][2]rune
}

// Match reports whether r is in the class. A negated class never
// matches '/'.
func (c Class) Match(r rune) bool {
	in := false
	for _, g := range c.ranges {
		if r >= g[0] && r <= g[1] {
			in = true
			break
		}
	}
	if c.neg {
		return !in && r != '/'
	}
	return in
}

// Parse parses a class starting at p[start], which must be '['.
// It returns the class and the index just past the closing ']'.
func Parse(p string, start int) (Class, int, error) {
	var c Class
	i := start + 1
	if i < len(p) && (p[i] == '!' || p[i] == '^') {
		c.neg = true
		i++
	}
	if i < len(p) && p[i] == ']' {
		if i+1 >= len(p) {
			return c, 0, &Error{KindEmpty, start}
		}
		c.ranges = append(c.ranges, [2]rune{']', ']'})
		i++
	}
	for {
		if i >= len(p) {
			return c, 0, &Error{KindUnclosed, start}
		}
		if p[i] == ']' {
			return c, i + 1, nil
		}
		lo, next := classChar(p, i)
		i = next
		if i < len(p) && p[i] == '-' && i+1 < len(p) && p[i+1] != ']' {
			hi, after := classChar(p, i+1)
			if lo > hi {
				return c, 0, &Error{KindReversed, i}
			}
			c.ranges = append(c.ranges, [2]rune{lo, hi})
			i = after
		} else {
			c.ranges = append(c.ranges, [2]rune{lo, lo})
		}
	}
}

// classChar reads one class item: an escaped char or one code point.
func classChar(p string, i int) (rune, int) {
	if p[i] == '\\' && i+1 < len(p) {
		r, _, n := runes.Step(p, i+1)
		return r, n
	}
	r, _, n := runes.Step(p, i)
	return r, n
}
