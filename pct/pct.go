// Package pct validates and normalizes percent-escapes byte by byte.
//
// Normalization rules: hex letters in %XX are folded to uppercase; escapes
// of unreserved bytes (RFC 3986) are restored to the literal byte; escapes
// of any other byte are kept escaped. Decoded byte sequences must be valid
// UTF-8. Nothing else is rewritten.
package pct

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// ErrorKind classifies escape failures so callers can distinguish them.
type ErrorKind int

const (
	// ErrTruncated: '%' with fewer than two following characters.
	ErrTruncated ErrorKind = iota
	// ErrBadHex: '%' followed by a non-hexadecimal character.
	ErrBadHex
	// ErrBadUTF8: the decoded byte sequence is not valid UTF-8.
	ErrBadUTF8
)

// Error describes a malformed escape at a byte offset of the input.
type Error struct {
	Kind   ErrorKind
	Offset int
}

func (e *Error) Error() string {
	switch e.Kind {
	case ErrTruncated:
		return fmt.Sprintf("pct: truncated escape at byte %d", e.Offset)
	case ErrBadHex:
		return fmt.Sprintf("pct: non-hex escape at byte %d", e.Offset)
	default:
		return fmt.Sprintf("pct: invalid UTF-8 from byte %d", e.Offset)
	}
}

const upperHex = "0123456789ABCDEF"

// IsUnreserved reports whether b is an RFC 3986 unreserved byte.
func IsUnreserved(b byte) bool {
	switch {
	case 'A' <= b && b <= 'Z', 'a' <= b && b <= 'z', '0' <= b && b <= '9':
		return true
	}
	switch b {
	case '-', '.', '_', '~':
		return true
	}
	return false
}

func hexVal(b byte) (int, bool) {
	switch {
	case '0' <= b && b <= '9':
		return int(b - '0'), true
	case 'a' <= b && b <= 'f':
		return int(b-'a') + 10, true
	case 'A' <= b && b <= 'F':
		return int(b-'A') + 10, true
	}
	return 0, false
}

// Normalize validates every escape in s and returns the normalized form.
// On error it returns no partial result.
func Normalize(s string) (string, error) {
	if strings.IndexByte(s, '%') < 0 {
		if !utf8.ValidString(s) {
			return "", &Error{Kind: ErrBadUTF8, Offset: firstBadRune(s)}
		}
		return s, nil
	}
	out := make([]byte, 0, len(s))
	dec := make([]byte, 0, len(s)) // fully decoded bytes
	org := make([]int, 0, len(s))  // origin offset per decoded byte
	for i := 0; i < len(s); {
		c := s[i]
		if c != '%' {
			out = append(out, c)
			dec = append(dec, c)
			org = append(org, i)
			i++
			continue
		}
		if i+2 >= len(s) {
			return "", &Error{Kind: ErrTruncated, Offset: i}
		}
		hi, ok1 := hexVal(s[i+1])
		lo, ok2 := hexVal(s[i+2])
		if !ok1 || !ok2 {
			return "", &Error{Kind: ErrBadHex, Offset: i}
		}
		b := byte(hi<<4 | lo)
		dec = append(dec, b)
		org = append(org, i)
		if IsUnreserved(b) {
			out = append(out, b)
		} else {
			out = append(out, '%', upperHex[hi], upperHex[lo])
		}
		i += 3
	}
	for j := 0; j < len(dec); {
		r, size := utf8.DecodeRune(dec[j:])
		if r == utf8.RuneError && size == 1 {
			return "", &Error{Kind: ErrBadUTF8, Offset: org[j]}
		}
		j += size
	}
	return string(out), nil
}

func firstBadRune(s string) int {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return i
		}
		i += size
	}
	return 0
}
