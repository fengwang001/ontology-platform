// Package parse validates wildcard patterns. It depends on no other package.
package parse

import (
	"errors"
	"unicode"
	"unicode/utf8"
)

// Limits are fixed for the process; rejected inputs must not change any state.
const (
	// MaxLen bounds the byte length of both a pattern and a text.
	MaxLen = 1 << 14
	// MaxWildcards bounds the total number of '?' and '*' in a pattern.
	MaxWildcards = 1 << 9
)

// Distinct, decidable sentinel errors. Callers discriminate with errors.Is.
var (
	// ErrUnsupportedChar: pattern contains a character other than
	// '?', '*' or an ordinary (non-control) character.
	ErrUnsupportedChar = errors.New("parse: unsupported character in pattern")
	// ErrInputTooLong: pattern or text exceeds MaxLen bytes.
	ErrInputTooLong = errors.New("parse: input exceeds maximum length")
	// ErrTooManyWildcards: '?' and '*' together exceed MaxWildcards.
	ErrTooManyWildcards = errors.New("parse: too many wildcard characters")
)

// Validate reports whether pattern is legal: its length is within MaxLen,
// it holds at most MaxWildcards wildcards, and every rune is '?', '*' or a
// non-control ordinary character. Checks are evaluated in a fixed order so
// the returned error is deterministic. Validate is side-effect free.
func Validate(pattern string) error {
	if len(pattern) > MaxLen {
		return ErrInputTooLong
	}
	wild := 0
	for i := 0; i < len(pattern); {
		r, size := utf8.DecodeRuneInString(pattern[i:])
		if r == '?' || r == '*' {
			wild++
			if wild > MaxWildcards {
				return ErrTooManyWildcards
			}
		} else if (r == utf8.RuneError && size == 1) || !unicode.IsGraphic(r) {
			// size==1 with RuneError means an invalid UTF-8 byte; every
			// other ordinary character must be printable/visible.
			return ErrUnsupportedChar
		}
		i += size
	}
	return nil
}

// ValidateText reports whether s may be matched: only its length is bounded.
func ValidateText(s string) error {
	if len(s) > MaxLen {
		return ErrInputTooLong
	}
	return nil
}
