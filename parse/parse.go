// Package parse validates wildcard patterns. It must not import any other
// package of this module.
package parse

import (
	"errors"
	"unicode"
	"unicode/utf8"
)

// Limits enforced on every pattern (and, for MaxLen, on every text).
const (
	MaxLen       = 4096
	MaxWildcards = 64
)

// Distinct, decidable sentinel errors. Callers must use errors.Is.
var (
	// ErrUnsupportedChar: the pattern contains a rune other than '?', '*'
	// or an ordinary printable rune (control runes / invalid UTF-8 rejected).
	ErrUnsupportedChar = errors.New("parse: unsupported character in pattern")
	// ErrTooLong: pattern or text exceeds MaxLen runes.
	ErrTooLong = errors.New("parse: input too long")
	// ErrTooManyWildcards: the pattern carries more than MaxWildcards
	// '?' / '*' runes.
	ErrTooManyWildcards = errors.New("parse: too many wildcards")
)

// Validate reports whether p is a legal pattern: at most MaxLen runes,
// only '?', '*' and printable ordinary runes, at most MaxWildcards
// wildcards. The three failure kinds map to three distinct sentinels.
func Validate(p string) error {
	if utf8.RuneCountInString(p) > MaxLen {
		return ErrTooLong
	}
	wildcards := 0
	for _, r := range p {
		switch {
		case r == '?' || r == '*':
			wildcards++
		case r == utf8.RuneError || !unicode.IsPrint(r):
			return ErrUnsupportedChar
		}
	}
	if wildcards > MaxWildcards {
		return ErrTooManyWildcards
	}
	return nil
}

// ValidateText reports whether a match text is admissible: any rune is
// allowed, only the length bound applies.
func ValidateText(s string) error {
	if utf8.RuneCountInString(s) > MaxLen {
		return ErrTooLong
	}
	return nil
}
