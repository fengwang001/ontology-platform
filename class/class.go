// Package class parses and matches [...] character classes: negation
// ([!...] and [^...]), ranges, escapes, and a literal ] as first item.
package class

import (
	"errors"
	"fmt"

	"ontology/runes"
)

var (
	ErrUnterminated = errors.New("unterminated character class")
	ErrEmpty        = errors.New("empty character class")
	ErrReversed     = errors.New("reversed range")
)

// Error is a parse failure carrying the byte offset in the pattern.
type Error struct {
	Kind   error
	Offset int
}

func (e *Error) Error() string { return fmt.Sprintf("%s at byte %d", e.Kind, e.Offset) }
func (e *Error) Unwrap() error { return e.Kind }

// Class is a compiled character class.
type Class struct {
	negate bool
	ranges [][2]rune // inclusive; a single char is [r, r]
}

// Parse parses a class starting at b[i] == '[' and returns the class
// plus the index just past the closing ']'.
func Parse(b []byte, i int) (*Class, int, error) {
	c := &Class{}
	j := i + 1
	if j < len(b) && (b[j] == '!' || b[j] == '^') {
		c.negate = true
		j++
	}
	if j+1 >= len(b) && j < len(b) && b[j] == ']' {
		return nil, 0, &Error{Kind: ErrEmpty, Offset: i}
	}
	first := true
	pending := rune(-1) // low char waiting to see if a '-' follows
	for {
		if j >= len(b) {
			return nil, 0, &Error{Kind: ErrUnterminated, Offset: i}
		}
		if b[j] == ']' && !first {
			c.add(pending)
			return c, j + 1, nil
		}
		first = false
		r, size := item(b, j)
		j += size
		if r == '-' && pending >= 0 {
			if j >= len(b) || b[j] == ']' { // trailing '-' is literal: [a-]
				c.add(pending, '-')
				pending = -1
				continue
			}
			hi, sz := item(b, j)
			if hi < pending {
				return nil, 0, &Error{Kind: ErrReversed, Offset: j}
			}
			j += sz
			c.addRange(pending, hi)
			pending = -1
			continue
		}
		c.add(pending)
		pending = r
	}
}

func (c *Class) add(rs ...rune) {
	for _, r := range rs {
		if r >= 0 {
			c.ranges = append(c.ranges, [2]rune{r, r})
		}
	}
}

func (c *Class) addRange(lo, hi rune) { c.ranges = append(c.ranges, [2]rune{lo, hi}) }

// item reads one class member at b[j], resolving backslash escapes.
func item(b []byte, j int) (rune, int) {
	if b[j] == '\\' && j+1 < len(b) {
		r, size := runes.Decode(b, j+1)
		return r, 1 + size
	}
	return runes.Decode(b, j)
}

// Match reports whether r is in the class. A negated class never
// matches '/'.
func (c *Class) Match(r rune) bool {
	in := false
	for _, rg := range c.ranges {
		if r >= rg[0] && r <= rg[1] {
			in = true
			break
		}
	}
	if c.negate {
		return !in && r != '/'
	}
	return in
}
