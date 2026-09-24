// Package pattern parses and normalizes SQL LIKE-style wildcard patterns.
// Metacharacters: '%' (any run of codepoints, including empty), '_' (one
// codepoint). Backslash escapes '%', '_' and itself. All positions are handled
// in Unicode codepoints; byte offsets are reported only in errors.
package pattern

import (
	"errors"
	"fmt"
	"unicode/utf8"
)

// Sentinel errors distinguishing the offending side of a match.
var (
	ErrPattern = errors.New("invalid pattern")
	ErrText    = errors.New("invalid text")
)

// PosError is a decidable error carrying a byte position.
type PosError struct {
	Kind error // ErrPattern or ErrText
	Pos  int   // byte offset at which the problem starts
	Msg  string
}

// Error implements the error interface.
func (e *PosError) Error() string {
	return fmt.Sprintf("%s at byte %d: %s", e.Kind, e.Pos, e.Msg)
}

// Unwrap enables errors.Is(err, ErrPattern) / errors.Is(err, ErrText).
func (e *PosError) Unwrap() error { return e.Kind }

// BytePos reports the byte offset attached to the error.
func (e *PosError) BytePos() int { return e.Pos }

// Kind is the role of a normalized pattern token.
type Kind int

const (
	// Literal matches exactly one codepoint R.
	Literal Kind = iota
	// AnyChar is '_': exactly one codepoint.
	AnyChar
	// AnySeq is '%': any codepoint sequence, including empty.
	AnySeq
)

// Token is one normalized pattern element.
type Token struct {
	Kind Kind
	R    rune // meaningful only for Literal
}

// Pattern is a parsed, normalized pattern.
type Pattern struct {
	tokens []Token
}

// Parse decodes s into tokens, applying escapes and folding adjacent '%'.
func Parse(s string) (*Pattern, error) {
	p := &Pattern{}
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size < 2 {
			return nil, &PosError{Kind: ErrPattern, Pos: i, Msg: "illegal UTF-8 encoding"}
		}
		switch r {
		case '\\':
			if i+size == len(s) {
				return nil, &PosError{Kind: ErrPattern, Pos: i, Msg: "dangling escape backslash"}
			}
			nr, nsize := utf8.DecodeRuneInString(s[i+size:])
			if nr == utf8.RuneError && nsize < 2 {
				return nil, &PosError{Kind: ErrPattern, Pos: i + size, Msg: "illegal UTF-8 encoding"}
			}
			if nr != '%' && nr != '_' && nr != '\\' {
				return nil, &PosError{Kind: ErrPattern, Pos: i, Msg: "backslash may only escape %, _ or \\"}
			}
			p.tokens = append(p.tokens, Token{Kind: Literal, R: nr})
			i += size + nsize
		case '%':
			if len(p.tokens) == 0 || p.tokens[len(p.tokens)-1].Kind != AnySeq {
				p.tokens = append(p.tokens, Token{Kind: AnySeq})
			}
			i += size
		case '_':
			p.tokens = append(p.tokens, Token{Kind: AnyChar})
			i += size
		default:
			p.tokens = append(p.tokens, Token{Kind: Literal, R: r})
			i += size
		}
	}
	return p, nil
}

// Tokens returns the normalized token sequence.
func (p *Pattern) Tokens() []Token { return p.tokens }

// Len is the normalized pattern length in tokens (codepoint units).
func (p *Pattern) Len() int { return len(p.tokens) }

// NewTextError builds a text-side positional error.
func NewTextError(pos int, msg string) *PosError {
	return &PosError{Kind: ErrText, Pos: pos, Msg: msg}
}
