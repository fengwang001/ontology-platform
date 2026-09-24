// Package match implements SQL LIKE-style wildcard matching with a linear,
// single-backtrack-anchor scan. Steps are reported through an optional
// *budget.Counter; the matcher is a pure function of (pattern, text).
package match

import (
	"unicode/utf8"

	"ontology/budget"
	"ontology/pattern"
)

// Match reports whether text matches the LIKE pattern. A nil counter means
// unlimited steps. Errors wrap pattern.ErrPattern, pattern.ErrText or
// budget.ErrBudget and are decidable with errors.Is.
func Match(pat, text string, c *budget.Counter) (bool, error) {
	p, err := pattern.Parse(pat)
	if err != nil {
		return false, err
	}
	return MatchParsed(p, text, c)
}

// MatchParsed runs the matcher on an already parsed, normalized pattern.
func MatchParsed(p *pattern.Pattern, text string, c *budget.Counter) (bool, error) {
	runes, err := decodeText(text)
	if err != nil {
		return false, err
	}
	tokens := p.Tokens()

	pi, ti := 0, 0
	starPat, starText := -1, -1 // single backtrack anchor
	for ti < len(runes) {
		if err := c.Tick(); err != nil {
			return false, err
		}
		switch {
		case pi < len(tokens) && tokens[pi].Kind == pattern.AnySeq:
			starPat, starText = pi, ti
			pi++ // '%' first consumes zero codepoints
		case pi < len(tokens) && fits(tokens[pi], runes[ti]):
			pi++
			ti++
		case starPat >= 0:
			pi = starPat + 1
			starText++ // make the anchored '%' consume one more codepoint
			ti = starText
		default:
			return false, nil
		}
	}
	for pi < len(tokens) {
		if err := c.Tick(); err != nil {
			return false, err
		}
		if tokens[pi].Kind != pattern.AnySeq {
			return false, nil
		}
		pi++
	}
	return true, nil
}

func fits(tok pattern.Token, r rune) bool {
	switch tok.Kind {
	case pattern.AnyChar:
		return true
	case pattern.Literal:
		return tok.R == r
	default:
		return false
	}
}

func decodeText(s string) ([]rune, error) {
	runes := make([]rune, 0, len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size < 2 {
			return nil, pattern.NewTextError(i, "illegal UTF-8 encoding")
		}
		runes = append(runes, r)
		i += size
	}
	return runes, nil
}
