// Package parse validates regex patterns built from literal
// characters, '.' and '*'. It has no dependencies on the other
// packages of this module.
package parse

import "errors"

// MaxLen is the maximum accepted length of a pattern or a text.
const MaxLen = 1 << 20

// Sentinel errors, mutually distinguishable via errors.Is.
var (
	ErrSyntax      = errors.New("parse: invalid pattern syntax")   // leading '*' or "**"
	ErrUnsupported = errors.New("parse: unsupported character")    // not '.', '*', letter or digit
	ErrTooLong     = errors.New("parse: input exceeds max length") // pattern or text over MaxLen
)

// Validate checks that p is a legal pattern: within the length
// limit, every byte is a supported character, and every '*' has a
// preceding literal or '.' to bind to (no leading '*', no "**").
// It is pure: a rejected pattern changes no state anywhere.
func Validate(p string) error {
	if len(p) > MaxLen {
		return ErrTooLong
	}
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c == '*':
			if i == 0 || p[i-1] == '*' {
				return ErrSyntax
			}
		case c == '.' || isLiteral(c):
		default:
			return ErrUnsupported
		}
	}
	return nil
}

func isLiteral(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
