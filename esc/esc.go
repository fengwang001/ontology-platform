// Package esc classifies a single JSON escape sequence, including the
// combination and validation of UTF-16 surrogate pairs. It depends on
// nothing outside the standard library.
package esc

import "errors"

// Sentinel errors classifying a malformed escape sequence.
var (
	ErrUnknownEscape = errors.New("esc: unknown escape")
	ErrShortHex      = errors.New("esc: \\u requires 4 hex digits")
	ErrLoneSurrogate = errors.New("esc: unpaired surrogate")
)

// Parser incrementally parses one escape sequence. Bytes following the
// leading backslash are fed one at a time; each byte is examined once.
type Parser struct {
	st   int
	hi   rune // pending high surrogate awaiting its low surrogate
	acc  rune // accumulated hex value of the current \uXXXX unit
	nhex int  // hex digits accumulated so far
}

// Parser states.
const (
	stLetter = iota // expect the escape letter
	stHex1          // inside the first \uXXXX unit
	stSlash         // after a high surrogate: expect '\'
	stU             // after that backslash: expect 'u'
	stHex2          // inside the second \uXXXX unit
)

// New returns a Parser positioned just after the leading backslash.
func New() *Parser { return &Parser{} }

// Feed consumes one byte. done reports a complete escape yielding r.
func (p *Parser) Feed(c byte) (r rune, done bool, err error) {
	switch p.st {
	case stLetter:
		switch c {
		case '"':
			return '"', true, nil
		case '\\':
			return '\\', true, nil
		case '/':
			return '/', true, nil
		case 'b':
			return '\b', true, nil
		case 'f':
			return '\f', true, nil
		case 'n':
			return '\n', true, nil
		case 'r':
			return '\r', true, nil
		case 't':
			return '\t', true, nil
		case 'u':
			p.st = stHex1
		default:
			return 0, false, ErrUnknownEscape
		}
	case stSlash:
		if c != '\\' {
			return 0, false, ErrLoneSurrogate
		}
		p.st = stU
	case stU:
		if c != 'u' {
			return 0, false, ErrLoneSurrogate
		}
		p.st = stHex2
	case stHex1, stHex2:
		h, ok := hexval(c)
		if !ok {
			if p.st == stHex1 {
				return 0, false, ErrShortHex
			}
			return 0, false, ErrLoneSurrogate
		}
		p.acc = p.acc<<4 | rune(h)
		if p.nhex++; p.nhex < 4 {
			return 0, false, nil
		}
		v := p.acc
		p.acc, p.nhex = 0, 0
		if p.st == stHex1 {
			switch {
			case v >= 0xD800 && v <= 0xDBFF:
				p.hi, p.st = v, stSlash
			case v >= 0xDC00 && v <= 0xDFFF:
				return 0, false, ErrLoneSurrogate
			default:
				return v, true, nil
			}
			return 0, false, nil
		}
		if v < 0xDC00 || v > 0xDFFF {
			return 0, false, ErrLoneSurrogate
		}
		return 0x10000 + (p.hi-0xD800)<<10 + (v - 0xDC00), true, nil
	}
	return 0, false, nil
}

// End reports the error for a stream ending mid-escape. nil means only
// the bare backslash was seen (the literal is simply unterminated).
func (p *Parser) End() error {
	switch p.st {
	case stHex1:
		return ErrShortHex
	case stSlash, stU, stHex2:
		return ErrLoneSurrogate
	}
	return nil
}

func hexval(c byte) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10, true
	}
	return 0, false
}
