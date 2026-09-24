// Package jstr strictly decodes and minimally encodes JSON string
// literals (input to Decode includes both double quotes). It never
// substitutes U+FFFD: invalid input is always a reported error.
package jstr

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"ontology/esc"
)

// Kind identifies a distinguishable failure category.
type Kind int

const (
	NoOpen    Kind = iota // missing opening quote
	Control               // unescaped control character U+0000–U+001F
	Escape                // unknown escape sequence
	Hex                   // \u not followed by 4 hex digits
	NoClose               // missing closing quote
	Trailing              // bytes after the closing quote
	BadUTF8               // invalid UTF-8 (literal bytes or Encode input)
	Surrogate             // unpaired or misordered UTF-16 surrogate
)

var kindName = [...]string{
	NoOpen: "missing opening quote", Control: "unescaped control character",
	Escape: "unknown escape", Hex: "bad \\u hex", NoClose: "missing closing quote",
	Trailing: "trailing bytes", BadUTF8: "invalid UTF-8", Surrogate: "bad surrogate",
}

// Error is the sole error type; Kind and absolute byte offset are set.
type Error struct {
	Kind Kind
	Off  int
}

func (e *Error) Error() string { return kindName[e.Kind] + " at byte " + strconv.Itoa(e.Off) }

var examined int64

// Examined reports the total number of input bytes inspected so far.
func Examined() int64 { return examined }

// ResetExamined zeroes the inspection counter.
func ResetExamined() { examined = 0 }

const (
	stOpen = iota
	stText
	stEsc
	stU
	stHi
	stDone
)

// Decoder is a streaming strict decoder: feed chunks with Write,
// finish with Close. Byte offsets are absolute across chunks.
type Decoder struct {
	out    []byte
	state  int
	off    int
	escAt  int
	hiAt   int
	hi     uint16
	uval   uint16
	ucount int
	pend   [4]byte
	plen   int
	pneed  int
	runeAt int
	pendHi bool
}

// Write consumes one chunk of the literal.
func (d *Decoder) Write(p []byte) error {
	for _, b := range p {
		examined++
		if err := d.step(b); err != nil {
			return err
		}
		d.off++
	}
	return nil
}

func (d *Decoder) step(b byte) error {
	switch d.state {
	case stOpen:
		if b != '"' {
			return &Error{NoOpen, d.off}
		}
		d.state = stText
	case stDone:
		return &Error{Trailing, d.off}
	case stText:
		return d.text(b)
	case stEsc:
		return d.escaped(b)
	case stU:
		return d.hex(b)
	case stHi: // high surrogate seen: only a `\uDC00–\uDFFF` may follow
		if b != '\\' {
			return &Error{Surrogate, d.hiAt}
		}
		d.state = stEsc
	}
	return nil
}

func (d *Decoder) escaped(b byte) error {
	if d.pendHi {
		if b != 'u' {
			return &Error{Surrogate, d.hiAt}
		}
		d.state, d.ucount, d.uval = stU, 0, 0
		return nil
	}
	if c, ok := esc.Simple(b); ok {
		d.out = append(d.out, c)
		d.state = stText
	} else if b == 'u' {
		d.state, d.ucount, d.uval = stU, 0, 0
	} else {
		return &Error{Escape, d.off}
	}
	return nil
}

func (d *Decoder) hex(b byte) error {
	v, ok := esc.Hex(b)
	if !ok {
		return &Error{Hex, d.off}
	}
	if d.uval, d.ucount = d.uval<<4|v, d.ucount+1; d.ucount < 4 {
		return nil
	}
	d.state = stText
	if d.pendHi {
		d.pendHi = false
		if !esc.IsLow(d.uval) {
			return &Error{Surrogate, d.hiAt}
		}
		d.out = utf8.AppendRune(d.out, esc.Combine(d.hi, d.uval))
		return nil
	}
	switch {
	case esc.IsHigh(d.uval):
		d.hi, d.hiAt, d.pendHi, d.state = d.uval, d.escAt, true, stHi
	case esc.IsLow(d.uval):
		return &Error{Surrogate, d.escAt}
	default:
		d.out = utf8.AppendRune(d.out, rune(d.uval))
	}
	return nil
}

func (d *Decoder) text(b byte) error {
	switch {
	case d.pneed > 0:
		if b&0xC0 != 0x80 {
			return &Error{BadUTF8, d.off}
		}
		d.pend[d.plen], d.plen, d.pneed = b, d.plen+1, d.pneed-1
		if d.pneed > 0 {
			return nil
		}
		if r, _ := utf8.DecodeRune(d.pend[:d.plen]); r == utf8.RuneError {
			return &Error{BadUTF8, d.runeAt}
		}
		d.out = append(d.out, d.pend[:d.plen]...)
		d.plen = 0
	case b == '"':
		d.state = stDone
	case b == '\\':
		d.state, d.escAt = stEsc, d.off
	case b < 0x20:
		return &Error{Control, d.off}
	case b < 0x80:
		d.out = append(d.out, b)
	case b >= 0xC2 && b <= 0xDF:
		d.pend[0], d.plen, d.pneed, d.runeAt = b, 1, 1, d.off
	case b >= 0xE0 && b <= 0xEF:
		d.pend[0], d.plen, d.pneed, d.runeAt = b, 1, 2, d.off
	case b >= 0xF0 && b <= 0xF4:
		d.pend[0], d.plen, d.pneed, d.runeAt = b, 1, 3, d.off
	default:
		return &Error{BadUTF8, d.off}
	}
	return nil
}

// Close finishes decoding; the complete literal must have been written.
func (d *Decoder) Close() (string, error) {
	switch d.state {
	case stDone:
		return string(d.out), nil
	case stU:
		return "", &Error{Hex, d.off}
	case stOpen:
		return "", &Error{NoOpen, 0}
	case stHi, stEsc:
		if d.pendHi {
			return "", &Error{Surrogate, d.hiAt}
		}
	}
	return "", &Error{NoClose, d.off}
}

// Decode strictly decodes one complete JSON string literal.
func Decode(lit []byte) (string, error) {
	d := new(Decoder)
	if err := d.Write(lit); err != nil {
		return "", err
	}
	return d.Close()
}

// Encode minimally escapes s into a JSON string literal: only `"`, `\`
// and U+0000–U+001F are escaped (short forms for \b \f \n \r \t, else
// \u00xx lowercase). It returns a *Error of kind BadUTF8 when s is not
// valid UTF-8; it never emits U+FFFD replacements.
func Encode(s string) ([]byte, error) {
	for i := 0; i < len(s); {
		if r, n := utf8.DecodeRuneInString(s[i:]); r == utf8.RuneError && n == 1 {
			return nil, &Error{BadUTF8, i}
		} else {
			i += n
		}
	}
	out := make([]byte, 1, len(s)+8)
	out[0] = '"'
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"' || c == '\\':
			out = append(out, '\\', c)
		case c < 0x20:
			out = appendCtrl(out, c)
		default:
			out = append(out, c)
		}
	}
	return append(out, '"'), nil
}

func appendCtrl(out []byte, c byte) []byte {
	if i := strings.IndexByte("\b\f\n\r\t", c); i >= 0 {
		return append(out, '\\', "bfnrt"[i])
	}
	const hexd = "0123456789abcdef"
	return append(out, '\\', 'u', '0', '0', hexd[c>>4], hexd[c&0xF])
}
