package jstr

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"ontology/esc"
)

var (
	ErrControlCharacter = errors.New("unescaped control character")
	ErrUnknownEscape    = errors.New("unknown escape")
	ErrInvalidUnicode   = errors.New("invalid unicode escape")
	ErrMissingQuote     = errors.New("missing closing quote")
	ErrTrailingBytes    = errors.New("bytes after closing quote")
	ErrInvalidUTF8      = errors.New("invalid UTF-8")
	ErrLoneSurrogate    = errors.New("lone UTF-16 surrogate")
)

type DecodeError struct {
	Err    error
	Offset int
}

func (e *DecodeError) Error() string {
	return fmt.Sprintf("%s at byte offset %d", e.Err, e.Offset)
}

func (e *DecodeError) Unwrap() error { return e.Err }

type EncodeError struct {
	Offset int
}

func (e *EncodeError) Error() string {
	return fmt.Sprintf("%s at byte offset %d", ErrInvalidUTF8, e.Offset)
}

func (e *EncodeError) Unwrap() error { return ErrInvalidUTF8 }

const (
	stateStart = iota
	stateText
	stateEscape
	stateUnicode
	stateNeedPairSlash
	stateNeedPairU
	statePairHex
	stateDone
	stateBad
)

type Decoder struct {
	value       strings.Builder
	state       int
	offset      int
	checked     int
	escapeStart int
	hexN        int
	hex         rune
	high        rune
	highStart   int
	utfStart    int
	utfLen      int
	utfN        int
	utfRemain   int
	utfMin      byte
	utfMax      byte
	pending     [4]byte
	err         error
}

func NewDecoder() *Decoder { return &Decoder{} }

func (d *Decoder) Write(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	for i, b := range p {
		d.checked++
		offset := d.offset
		d.offset++
		if d.state == stateStart {
			if b != '"' {
				return i, d.fail(ErrMissingQuote, 0)
			}
			d.state = stateText
			continue
		}
		if d.state == stateDone {
			return i, d.fail(ErrTrailingBytes, offset)
		}
		if err := d.writeByte(b, offset); err != nil {
			return i, err
		}
	}
	return len(p), nil
}

func (d *Decoder) writeByte(b byte, offset int) error {
	switch d.state {
	case stateText:
		return d.writeTextByte(b, offset)
	case stateEscape:
		if r, ok := esc.DecodeSimple(b); ok {
			d.value.WriteRune(r)
			d.state = stateText
			return nil
		}
		if b == 'u' {
			d.escapeStart, d.hexN, d.hex = offset-1, 0, 0
			d.state = stateUnicode
			return nil
		}
		return d.fail(ErrUnknownEscape, offset-1)
	case stateUnicode, statePairHex:
		return d.writeHex(b)
	case stateNeedPairSlash:
		if b == '\\' {
			d.state = stateNeedPairU
			return nil
		}
		return d.fail(ErrLoneSurrogate, d.highStart)
	case stateNeedPairU:
		if b == 'u' {
			d.hexN, d.hex = 0, 0
			d.state = statePairHex
			return nil
		}
		return d.fail(ErrLoneSurrogate, d.highStart)
	}
	return d.fail(ErrMissingQuote, offset)
}

func (d *Decoder) writeTextByte(b byte, offset int) error {
	if d.utfRemain > 0 {
		if b < d.utfMin || b > d.utfMax {
			return d.fail(ErrInvalidUTF8, d.utfStart)
		}
		d.pending[d.utfN] = b
		d.utfN++
		d.utfRemain--
		d.utfMin, d.utfMax = 0xA0, 0xBF
		if d.utfRemain == 0 {
			d.value.Write(d.pending[:d.utfLen])
		}
		return nil
	}
	switch {
	case b == '"':
		d.state = stateDone
	case b == '\\':
		d.state = stateEscape
	case b < 0x20:
		return d.fail(ErrControlCharacter, offset)
	case b < 0x80:
		d.value.WriteByte(b)
	default:
		return d.startUTF8(b, offset)
	}
	return nil
}

func (d *Decoder) startUTF8(b byte, offset int) error {
	var length int
	var min, max byte = 0xA0, 0xBF
	switch {
	case 0xC2 <= b && b <= 0xDF:
		length = 1
	case b == 0xE0:
		length, min = 2, 0xA0
	case 0xE1 <= b && b <= 0xEC:
		length = 2
	case b == 0xED:
		length, min, max = 2, 0x80, 0x9F
	case 0xEE <= b && b <= 0xEF:
		length = 2
	case b == 0xF0:
		length, min = 3, 0x90
	case 0xF1 <= b && b <= 0xF3:
		length = 3
	case b == 0xF4:
		length, min, max = 3, 0x80, 0x8F
	default:
		return d.fail(ErrInvalidUTF8, offset)
	}
	d.utfStart, d.utfLen, d.utfN, d.utfRemain = offset, length+1, 1, length
	d.utfMin, d.utfMax, d.pending[0] = min, max, b
	return nil
}

func (d *Decoder) writeHex(b byte) error {
	v, ok := esc.DecodeHexDigit(b)
	pair := d.state == statePairHex
	if !ok {
		if pair {
			return d.fail(ErrLoneSurrogate, d.highStart)
		}
		return d.fail(ErrInvalidUnicode, d.escapeStart)
	}
	d.hex = d.hex<<4 | rune(v)
	d.hexN++
	if d.hexN < 4 {
		return nil
	}
	if pair {
		r, ok := esc.DecodePair(d.high, d.hex)
		if !ok {
			return d.fail(ErrLoneSurrogate, d.highStart)
		}
		d.value.WriteRune(r)
		d.state = stateText
		return nil
	}
	switch {
	case esc.IsHighSurrogate(d.hex):
		d.high, d.highStart = d.hex, d.escapeStart
		d.state = stateNeedPairSlash
	case esc.IsLowSurrogate(d.hex):
		return d.fail(ErrLoneSurrogate, d.escapeStart)
	default:
		d.value.WriteRune(d.hex)
		d.state = stateText
	}
	return nil
}

func (d *Decoder) Close() error {
	if d.err != nil {
		return d.err
	}
	switch d.state {
	case stateDone:
		return nil
	case stateNeedPairSlash, stateNeedPairU, statePairHex:
		return d.fail(ErrLoneSurrogate, d.highStart)
	case stateUnicode:
		return d.fail(ErrInvalidUnicode, d.escapeStart)
	}
	if d.utfRemain > 0 {
		return d.fail(ErrInvalidUTF8, d.utfStart)
	}
	return d.fail(ErrMissingQuote, d.offset)
}

func (d *Decoder) checks() int { return d.checked }

func (d *Decoder) fail(err error, offset int) error {
	d.err = &DecodeError{Err: err, Offset: offset}
	d.state = stateBad
	return d.err
}

func (d *Decoder) result() string { return d.value.String() }

func Decode(lit []byte) (string, error) {
	d := NewDecoder()
	if _, err := d.Write(lit); err != nil {
		return "", err
	}
	if err := d.Close(); err != nil {
		return "", err
	}
	return d.result(), nil
}

func Encode(s string) ([]byte, error) {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return nil, &EncodeError{Offset: i}
		}
		switch r {
		case '"':
			out = append(out, '\\', '"')
		case '\\':
			out = append(out, '\\', '\\')
		case '\b':
			out = append(out, '\\', 'b')
		case '\f':
			out = append(out, '\\', 'f')
		case '\n':
			out = append(out, '\\', 'n')
		case '\r':
			out = append(out, '\\', 'r')
		case '\t':
			out = append(out, '\\', 't')
		default:
			if r < 0x20 {
				const digits = "0123456789abcdef"
				out = append(out, '\\', 'u', '0', '0', digits[byte(r)>>4], digits[byte(r)])
			} else {
				out = append(out, s[i:i+size]...)
			}
		}
		i += size
	}
	out = append(out, '"')
	return out, nil
}
