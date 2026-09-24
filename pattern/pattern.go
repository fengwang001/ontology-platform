// Package pattern parses and normalizes SQL LIKE style patterns
// containing % (any run), _ (exactly one rune) and \ escapes.
package pattern

import (
	"errors"
	"fmt"
	"unicode/utf8"
)

// Error classes, distinguishable with errors.Is.
var (
	ErrInvalidEscape  = errors.New("invalid escape")
	ErrTrailingEscape = errors.New("trailing escape")
	ErrInvalidUTF8    = errors.New("invalid utf-8")
)

// EscapeError reports a bad escape sequence at byte Offset.
type EscapeError struct {
	Offset int
	Kind   error // ErrInvalidEscape or ErrTrailingEscape
}

func (e *EscapeError) Error() string {
	return fmt.Sprintf("pattern: %v at byte %d", e.Kind, e.Offset)
}

func (e *EscapeError) Unwrap() error { return e.Kind }

// UTF8Error reports invalid UTF-8 on Side ("pattern" or "text")
// at byte Offset.
type UTF8Error struct {
	Side   string
	Offset int
}

func (e *UTF8Error) Error() string {
	return fmt.Sprintf("%v in %s at byte %d", ErrInvalidUTF8, e.Side, e.Offset)
}

func (e *UTF8Error) Unwrap() error { return ErrInvalidUTF8 }

// CheckUTF8 returns a *UTF8Error if s is not valid UTF-8.
func CheckUTF8(side, s string) error {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size <= 1 {
			return &UTF8Error{Side: side, Offset: i}
		}
		i += size
	}
	return nil
}

// Kind is the token category.
type Kind int

const (
	Lit     Kind = iota // literal rune
	AnyOne              // _
	AnyMany             // %
)

// Token is one normalized pattern unit.
type Token struct {
	Kind Kind
	Lit  rune
}

// Pattern is a parsed, normalized pattern.
type Pattern struct {
	tokens []Token
	raw    string
}

// Parse parses s, folding adjacent % and resolving \ escapes.
func Parse(s string) (Pattern, error) {
	if err := CheckUTF8("pattern", s); err != nil {
		return Pattern{}, err
	}
	p := Pattern{raw: s}
	for i := 0; i < len(s); {
		switch s[i] {
		case '\\':
			if i+1 >= len(s) {
				return Pattern{}, &EscapeError{Offset: i, Kind: ErrTrailingEscape}
			}
			c := s[i+1]
			if c != '%' && c != '_' && c != '\\' {
				return Pattern{}, &EscapeError{Offset: i, Kind: ErrInvalidEscape}
			}
			p.tokens = append(p.tokens, Token{Kind: Lit, Lit: rune(c)})
			i += 2
		case '%':
			if n := len(p.tokens); n == 0 || p.tokens[n-1].Kind != AnyMany {
				p.tokens = append(p.tokens, Token{Kind: AnyMany})
			}
			i++
		case '_':
			p.tokens = append(p.tokens, Token{Kind: AnyOne})
			i++
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			p.tokens = append(p.tokens, Token{Kind: Lit, Lit: r})
			i += size
		}
	}
	return p, nil
}

// Len returns the normalized token count (after % folding).
func (p Pattern) Len() int { return len(p.tokens) }

// Tokens returns the normalized tokens. Callers must not mutate them.
func (p Pattern) Tokens() []Token { return p.tokens }

// String returns the raw pattern text.
func (p Pattern) String() string { return p.raw }
