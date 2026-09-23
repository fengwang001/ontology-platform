// Package class parses and matches [...] character classes.
package class

import "ontology/runes"

// Term is one class member: a literal code point or an inclusive range.
type Term struct {
	Lo, Hi runes.R
	Range  bool
}

// Class is a parsed character class.
type Class struct {
	Negate bool
	Terms  []Term
}

// PosError carries a byte offset into the offending pattern.
type PosError struct {
	Kind error
	Pos  int
	Msg  string
}

func (e *PosError) Error() string { return e.Msg }
func (e *PosError) Unwrap() error { return e.Kind }

type errorString string

func (e errorString) Error() string { return string(e) }

// Sentinel, mutually distinguishable error kinds.
var (
	ErrUnclosedClass = errorString("unclosed character class")
	ErrReversedRange = errorString("reversed range in character class")
	ErrTrailingSlash = errorString("pattern ends with a single backslash")
	ErrEmptyClass    = errorString("empty character class")
)

// ByteOff returns the byte offset of rune index j within rs.
func ByteOff(rs []runes.R, j int) int {
	off := 0
	for k := 0; k < j && k < len(rs); k++ {
		off += len(rs[k].Raw)
	}
	return off
}

// Parse reads one class in rs starting at rune index i (rs[i] == '[').
// It returns the class and the rune index just past the closing ']'.
// A ']' immediately after '[' or after the negate mark is a literal member,
// except when it also closes the class immediately ("[]" / "[!]").
func Parse(rs []runes.R, i int) (*Class, int, error) {
	c := &Class{}
	j := i + 1
	if j < len(rs) && (rs[j].Rune == '!' || rs[j].Rune == '^') {
		c.Negate = true
		j++
	}
	if j < len(rs) && rs[j].Rune == ']' {
		if j+1 < len(rs) && rs[j+1].Rune == ']' {
			c.Terms = append(c.Terms, Term{Lo: rs[j], Hi: rs[j]})
			j += 2
		}
	}
	for j < len(rs) && rs[j].Rune != ']' {
		lo, nj, err := readMember(rs, j)
		if err != nil {
			return nil, 0, err
		}
		j = nj
		hi, rng := lo, false
		if j < len(rs) && rs[j].Rune == '-' &&
			j+1 < len(rs) && rs[j+1].Rune != ']' {
			up, nj2, err := readMember(rs, j+1)
			if err != nil {
				return nil, 0, err
			}
			if lo.Rune > up.Rune {
				return nil, 0, &PosError{Kind: ErrReversedRange,
					Pos: ByteOff(rs, j-1), Msg: "reversed range"}
			}
			hi, rng, j = up, true, nj2
		}
		c.Terms = append(c.Terms, Term{Lo: lo, Hi: hi, Range: rng})
	}
	if j >= len(rs) {
		return nil, 0, &PosError{Kind: ErrUnclosedClass,
			Pos: ByteOff(rs, i), Msg: "unclosed class"}
	}
	if len(c.Terms) == 0 {
		return nil, 0, &PosError{Kind: ErrEmptyClass,
			Pos: ByteOff(rs, i), Msg: "empty class"}
	}
	return c, j + 1, nil
}

func readMember(rs []runes.R, j int) (runes.R, int, error) {
	if rs[j].Rune == '\\' {
		if j+1 >= len(rs) {
			return runes.R{}, 0, &PosError{Kind: ErrTrailingSlash,
				Pos: ByteOff(rs, j), Msg: "trailing backslash"}
		}
		return rs[j+1], j + 2, nil
	}
	return rs[j], j + 1, nil
}

// Match reports whether r satisfies the class. A negated class never matches
// the segment separator '/'.
func (c *Class) Match(r runes.R) bool {
	hit := false
	for _, t := range c.Terms {
		if (t.Range && r.Rune >= t.Lo.Rune && r.Rune <= t.Hi.Rune) ||
			(!t.Range && r.Eq(t.Lo)) {
			hit = true
			break
		}
	}
	if c.Negate {
		return !hit && r.Rune != '/'
	}
	return hit
}
