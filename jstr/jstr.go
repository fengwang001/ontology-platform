// Package jstr encodes and decodes a single strict RFC 8259 JSON string
// literal (the text including the surrounding double quotes). It depends
// only on the esc package and the standard library.
package jstr

import (
	"errors"
	"unicode/utf8"

	"ontology/esc"
)

// Sentinel errors, all mutually distinguishable via errors.Is.
var (
	ErrMissingQuote = errors.New("jstr: missing opening or closing quote")
	ErrTrailing     = errors.New("jstr: bytes after closing quote")
	ErrControl      = errors.New("jstr: unescaped control character")
	ErrBadEscape    = errors.New("jstr: unknown escape sequence")
	ErrShortUnicode = errors.New("jstr: \\u requires four hexadecimal digits")
	ErrBadSurrogate = errors.New("jstr: invalid UTF-16 surrogate pair")
	ErrInvalidUTF8  = errors.New("jstr: invalid UTF-8")
)

// DecodeError carries a sentinel Cause and the absolute byte offset
// (zero-based, counting the opening quote) of the offending lexical unit.
type DecodeError struct {
	Offset int
	Cause  error
}

func (e *DecodeError) Error() string { return e.Cause.Error() }
func (e *DecodeError) Unwrap() error { return e.Cause }

// Decode parses one complete JSON string literal including both quotes.
func Decode(lit []byte) (string, error) {
	d := NewDecoder()
	if _, err := d.Write(lit); err != nil {
		return "", err
	}
	return d.Close()
}

// Encode returns the minimal-escape JSON literal for s. It returns
// ErrInvalidUTF8 if s contains bytes that are not valid UTF-8; no U+FFFD
// substitution is ever performed.
func Encode(s string) ([]byte, error) {
	if !utf8.ValidString(s) {
		return nil, ErrInvalidUTF8
	}
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == '"' || r == '\\':
			out = append(out, '\\', byte(r))
		case r < 0x20:
			if c, ok := esc.ShortEscape(r); ok {
				out = append(out, '\\', c)
			} else {
				out = esc.AppendU4(out, r)
			}
		default:
			out = append(out, s[i:i+size]...)
		}
		i += size
	}
	out = append(out, '"')
	return out, nil
}

type state uint8

const (
	sBefore state = iota
	sText
	sEsc
	sU
	sWantLowEsc // high surrogate seen; next unit must start with '\'
	sWantLowU   // saw the '\'; must see 'u'
	sWantLowHex // reading low-surrogate hex digits
	sMB         // reading continuation bytes of one UTF-8 rune
	sAfter
)

// Decoder incrementally decodes one JSON string literal. Feed the whole
// literal (quotes included) through Write in any chunking, then call Close.
type Decoder struct {
	state      state
	out        []byte
	pos        int // offset of the next input byte
	start      int // offset where the current lexical unit begins
	hex        [4]byte
	hexN       int
	high       rune
	highOff    int // offset of a pending high surrogate's backslash
	mb         [utf8.UTFMax]byte
	mbHave     int
	mbNeed     int
	checked    int
	terminated bool
	err        *DecodeError
}

// NewDecoder returns an empty streaming decoder.
func NewDecoder() *Decoder { return &Decoder{} }

// Write feeds a chunk of the literal and returns the text decoded so far.
func (d *Decoder) Write(p []byte) (string, error) {
	for _, b := range p {
		d.feed(b)
	}
	if d.err != nil {
		return "", d.err
	}
	return string(d.out), nil
}

// Close signals end of input and validates termination.
func (d *Decoder) Close() (string, error) {
	if d.err != nil {
		return "", d.err
	}
	switch d.state {
	case sAfter:
		return string(d.out), nil
	case sWantLowEsc, sWantLowU:
		return "", d.fail(ErrBadSurrogate, d.highOff)
	case sU, sWantLowHex:
		return "", d.fail(ErrShortUnicode, d.start)
	case sMB:
		return "", d.fail(ErrInvalidUTF8, d.start)
	default:
		return "", d.fail(ErrMissingQuote, d.pos)
	}
}

// Checked returns the total number of input bytes examined so far.
func (d *Decoder) Checked() int { return d.checked }

func (d *Decoder) feed(b byte) {
	if d.err != nil {
		return
	}
	d.pos++
	d.checked++
	switch d.state {
	case sBefore:
		if b != '"' {
			d.fail(ErrMissingQuote, d.pos-1)
			return
		}
		d.state = sText
	case sText:
		d.feedText(b)
	case sEsc:
		d.feedEsc(b)
	case sU:
		d.feedHex(b)
	case sWantLowEsc:
		if b != '\\' {
			d.fail(ErrBadSurrogate, d.highOff)
			return
		}
		d.start = d.pos - 1
		d.state = sWantLowU
	case sWantLowU:
		d.feedWantLowU(b)
	case sWantLowHex:
		d.feedLowHex(b)
	case sMB:
		d.feedMB(b)
	case sAfter:
		d.fail(ErrTrailing, d.pos-1)
	}
}

func (d *Decoder) feedWantLowU(b byte) {
	if b == 'u' {
		d.hexN = 0
		d.state = sWantLowHex
		return
	}
	// A lexically valid simple escape still cannot be a low surrogate;
	// an invalid escape letter keeps its own ErrBadEscape.
	switch b {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		d.fail(ErrBadSurrogate, d.highOff)
	default:
		d.fail(ErrBadEscape, d.start)
	}
}

func (d *Decoder) feedText(b byte) {
	off := d.pos - 1
	switch {
	case b == '"':
		d.terminated = true
		d.state = sAfter
	case b == '\\':
		d.start = off
		d.state = sEsc
	case b < 0x20:
		d.fail(ErrControl, off)
	case b < 0x80:
		d.out = append(d.out, b)
	default:
		d.beginMB(b, off)
	}
}

func (d *Decoder) beginMB(b byte, off int) {
	d.start = off
	switch {
	case b&0xE0 == 0xC0:
		d.mbNeed = 2
	case b&0xF0 == 0xE0:
		d.mbNeed = 3
	case b&0xF8 == 0xF0:
		d.mbNeed = 4
	default:
		d.fail(ErrInvalidUTF8, off)
		return
	}
	d.mbHave, d.mb[0] = 1, byte(b)
	d.state = sMB
}

func (d *Decoder) feedMB(b byte) {
	off := d.pos - 1
	if b&0xC0 != 0x80 {
		d.fail(ErrInvalidUTF8, off)
		return
	}
	d.mb[d.mbHave] = b
	d.mbHave++
	if d.mbHave < d.mbNeed {
		return
	}
	if !utf8.Valid(d.mb[:d.mbNeed]) {
		d.fail(ErrInvalidUTF8, d.start)
		return
	}
	d.out = append(d.out, d.mb[:d.mbNeed]...)
	d.state = sText
}

func (d *Decoder) feedEsc(b byte) {
	switch b {
	case '"', '\\', '/':
		d.out = append(d.out, b)
	case 'b':
		d.out = append(d.out, '\b')
	case 'f':
		d.out = append(d.out, '\f')
	case 'n':
		d.out = append(d.out, '\n')
	case 'r':
		d.out = append(d.out, '\r')
	case 't':
		d.out = append(d.out, '\t')
	case 'u':
		d.hexN = 0
		d.state = sU
		return
	default:
		d.fail(ErrBadEscape, d.start)
		return
	}
	d.state = sText
}

func (d *Decoder) feedHex(b byte) {
	if !esc.IsHexDigit(b) {
		d.fail(ErrShortUnicode, d.start)
		return
	}
	d.hex[d.hexN] = b
	d.hexN++
	if d.hexN < 4 {
		return
	}
	cp := decodeHex(d.hex)
	if esc.IsLowSurrogate(cp) {
		d.fail(ErrBadSurrogate, d.start)
		return
	}
	if esc.IsHighSurrogate(cp) {
		d.high, d.highOff = cp, d.start
		d.state = sWantLowEsc
		return
	}
	d.emitRune(cp)
	d.state = sText
}

func (d *Decoder) feedLowHex(b byte) {
	if !esc.IsHexDigit(b) {
		d.fail(ErrShortUnicode, d.start)
		return
	}
	d.hex[d.hexN] = b
	d.hexN++
	if d.hexN < 4 {
		return
	}
	low := decodeHex(d.hex)
	if !esc.IsLowSurrogate(low) {
		d.fail(ErrBadSurrogate, d.highOff)
		return
	}
	d.emitRune(esc.Pair(d.high, low))
	d.state = sText
}

func (d *Decoder) emitRune(r rune) {
	var buf [utf8.UTFMax]byte
	n := utf8.EncodeRune(buf[:], r)
	d.out = append(d.out, buf[:n]...)
}

func decodeHex(h [4]byte) rune {
	return rune(esc.HexValue(h[0]))<<12 | rune(esc.HexValue(h[1]))<<8 |
		rune(esc.HexValue(h[2]))<<4 | rune(esc.HexValue(h[3]))
}

func (d *Decoder) fail(cause error, offset int) *DecodeError {
	if d.err == nil {
		d.err = &DecodeError{Offset: offset, Cause: cause}
	}
	return d.err
}
