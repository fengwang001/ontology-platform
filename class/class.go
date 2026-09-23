// Package class parses and matches [...] character classes.
package class

import "ontology/runes"

// Range is one inclusive code-point interval.
type Range struct{ Lo, Hi runes.Rune }

// Class is a parsed character class.
type Class struct {
	Negated bool
	Ranges  []Range
}

// Match reports whether r belongs to the class. Negated classes never match
// the path separator, per pattern semantics.
func (c *Class) Match(r runes.Rune) bool {
	hit := false
	for _, g := range c.Ranges {
		if runes.Eq(r, g.Lo) || runes.Eq(r, g.Hi) ||
			(r.Raw == 0 && g.Lo.Raw == 0 && g.Hi.Raw == 0 && r.R >= g.Lo.R && r.R <= g.Hi.R) {
			hit = true
			break
		}
	}
	if c.Negated {
		return !hit && r.R != '/'
	}
	return hit
}

// Parse consumes the body of a class; the caller already consumed '['.
// It stops after (and consumes) the closing ']'.
func Parse(cur *runes.Cursor) (*Class, error) {
	start := cur.Pos()
	c := &Class{}
	if r, ok := cur.Peek(); ok && (r.R == '!' || r.R == '^') {
		c.Negated = true
		cur.Next()
	}
	if r, ok := cur.Peek(); ok && r.R == ']' {
		cur.Next()
		c.Ranges = append(c.Ranges, Range{r, r})
	}
	for {
		r, ok := cur.Next()
		if !ok {
			return nil, &Error{Kind: ErrUnclosed, Offset: start}
		}
		if r.R == ']' {
			break
		}
		if r.R == '\\' {
			e, ok := cur.Next()
			if !ok {
				return nil, &Error{Kind: ErrTrailingEscape, Offset: cur.Pos()}
			}
			r = e
		}
		nr, ok := cur.Peek()
		if ok && nr.R == '-' {
			dash := cur.Pos()
			cur.Next()
			end, ok2 := cur.Peek()
			if !ok2 {
				return nil, &Error{Kind: ErrUnclosed, Offset: start}
			}
			if end.R == ']' {
				c.Ranges = append(c.Ranges, Range{r, r},
					Range{Lo: runes.Rune{R: '-'}, Hi: runes.Rune{R: '-'}})
				continue
			}
			cur.Next()
			if end.R == '\\' {
				esc, ok3 := cur.Next()
				if !ok3 {
					return nil, &Error{Kind: ErrTrailingEscape, Offset: cur.Pos()}
				}
				end = esc
			}
			if end.Raw != 0 || r.Raw != 0 || end.R < r.R {
				return nil, &Error{Kind: ErrReversedRange, Offset: dash}
			}
			c.Ranges = append(c.Ranges, Range{r, end})
			continue
		}
		c.Ranges = append(c.Ranges, Range{r, r})
	}
	if !c.Negated && len(c.Ranges) == 0 {
		return nil, &Error{Kind: ErrEmptyClass, Offset: start}
	}
	return c, nil
}

// Sentinel error kinds.
var (
	ErrUnclosed       = classErr("unclosed character class")
	ErrEmptyClass     = classErr("empty character class")
	ErrReversedRange  = classErr("reversed character range")
	ErrTrailingEscape = classErr("trailing backslash")
)

// Error is a class parse error carrying the byte offset in the pattern.
type Error struct {
	Kind   error
	Offset int
}

func (e *Error) Error() string { return e.Kind.Error() }
func (e *Error) Unwrap() error { return e.Kind }

type classErr string

func (e classErr) Error() string { return string(e) }
