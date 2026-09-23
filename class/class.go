// Package class parses and matches a single [...] character class.
package class

import (
	"errors"

	"ontology/runes"
)

// Sentinel errors for the decidable class syntax failures.
var (
	ErrUnterminated = errors.New("class: unterminated '['")
	ErrEmpty        = errors.New("class: empty class []")
	ErrReversed     = errors.New("class: reversed range [z-a]")
)

// Error carries a byte offset into the pattern together with its kind.
type Error struct {
	Offset int
	Kind   error
}

func (e *Error) Error() string { return e.Kind.Error() }
func (e *Error) Unwrap() error { return e.Kind }

type term struct {
	lit runes.Item // valid when single
	lo  runes.Item // valid when rng
	hi  runes.Item
	rng bool
	bad bool // either endpoint was an invalid byte
}

// Class is a parsed bracket expression.
type Class struct {
	Negate bool
	terms  []term
}

// Parse reads a class beginning at the '[' (s[start]=='[') and returns it
// together with the index just past the closing ']'.
func Parse(s string, start int) (*Class, int, error) {
	items := runes.Items(s)
	// Map code-point index -> byte offset of its first byte.
	off := make([]int, len(items)+1)
	bi := 0
	for k, it := range items {
		off[k] = bi
		bi += it.Bits
	}
	off[len(items)] = len(s)
	// Find the item index whose offset equals start.
	p := offsetIndex(off, start)
	if p >= len(items) || items[p].R != '[' {
		return nil, start, &Error{Offset: start, Kind: ErrUnterminated}
	}
	c := &Class{}
	p++
	if p < len(items) && (items[p].R == '!' || items[p].R == '^') {
		c.Negate = true
		p++
	}
	// A ']' as the first member is literal.
	if p < len(items) && items[p].R == ']' {
		c.terms = append(c.terms, term{lit: items[p]})
		p++
	}
	if p >= len(items) {
		return nil, off[p-1], &Error{Offset: off[p-1], Kind: ErrEmpty}
	}
	// Parse members until an unescaped ']'.
	for p < len(items) && items[p].R != ']' {
		t := term{}
		first, esc, adv := readMember(items, p)
		p += adv
		if p < len(items) && items[p].R == '-' &&
			p+1 < len(items) && items[p+1].R != ']' {
			p++ // consume '-'
			second, _, adv2 := readMember(items, p)
			p += adv2
			if (first.Bad || second.Bad) || runeVal(first) > runeVal(second) {
				if first.Bad || second.Bad {
					// Invalid-byte endpoints cannot define an ordered range.
					t = term{lit: first}
					c.terms = append(c.terms, t, term{lit: second})
					continue
				}
				return nil, off[p-adv2], &Error{Offset: off[p-adv2], Kind: ErrReversed}
			}
			t = term{lo: first, hi: second, rng: true}
		} else {
			_ = esc
			t = term{lit: first}
		}
		c.terms = append(c.terms, t)
	}
	if p >= len(items) {
		return nil, len(s), &Error{Offset: len(s), Kind: ErrUnterminated}
	}
	if len(c.terms) == 0 {
		return nil, off[2], &Error{Offset: start, Kind: ErrEmpty}
	}
	return c, off[p] + 1, nil
}

// readMember reads one possibly escaped member at item index p.
func readMember(items []runes.Item, p int) (runes.Item, bool, int) {
	if items[p].R == '\\' && p+1 < len(items) {
		return items[p+1], true, 2
	}
	return items[p], false, 1
}

func runeVal(it runes.Item) rune {
	if it.Bad {
		return -1
	}
	return it.R
}

func offsetIndex(off []int, start int) int {
	for i, o := range off {
		if o == start {
			return i
		}
	}
	return len(off)
}

// Match reports whether the class accepts the code point. A negated class
// never accepts '/'.
func (c *Class) Match(it runes.Item) bool {
	hit := false
	for _, t := range c.terms {
		if t.rng {
			if !it.Bad && !t.lo.Bad && !t.hi.Bad && it.R >= t.lo.R && it.R <= t.hi.R {
				hit = true
			}
		} else if runes.Eq(t.lit, it) {
			hit = true
		}
	}
	if c.Negate {
		if it.R == '/' && !it.Bad {
			return false
		}
		return !hit
	}
	return hit
}
