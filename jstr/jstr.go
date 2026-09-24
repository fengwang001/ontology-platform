package jstr

import (
	"unicode/utf8"

	"ontology/esc"
)

var (
	ErrControl   = SyntaxError{Kind: "control"}
	ErrEscape    = SyntaxError{Kind: "escape"}
	ErrHex       = SyntaxError{Kind: "hex"}
	ErrClose     = SyntaxError{Kind: "missing closing quote"}
	ErrTrailing  = SyntaxError{Kind: "trailing bytes"}
	ErrSurrogate = SyntaxError{Kind: "surrogate"}
)

type SyntaxError struct {
	Kind   string
	Offset int
}

func (e SyntaxError) Error() string { return "invalid JSON string: " + e.Kind }
func (e SyntaxError) Is(target error) bool {
	other, ok := target.(SyntaxError)
	return ok && other.Kind == e.Kind
}

type InvalidUTF8Error struct {
	Offset int
}

func (e InvalidUTF8Error) Error() string { return "invalid UTF-8 in JSON string" }

func Decode(lit []byte) (string, error) {
	d := NewDecoder()
	if _, err := d.Write(lit); err != nil {
		return "", err
	}
	if err := d.Close(); err != nil {
		return "", err
	}
	return d.String(), nil
}

// Encode returns a quoted JSON string. Invalid UTF-8 causes InvalidUTF8Error.
func Encode(s string) ([]byte, error) {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for offset, r := range s {
		if r == utf8.RuneError && utf8.RuneStart(s[offset]) {
			return nil, InvalidUTF8Error{Offset: offset}
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
				const hex = "0123456789abcdef"
				out = append(out, '\\', 'u', '0', '0', hex[r>>4], hex[r&15])
			} else {
				out = append(out, string(r)...)
			}
		}
	}
	return append(out, '"'), nil
}

type Decoder struct {
	dst       []byte
	checked   int
	offset    int
	state     uint8
	raw       []byte
	want      int
	hex       rune
	hexN      int
	high      rune
	highStart int
	err       error
}

const (
	stateOpen = iota
	stateValue
	stateEscape
	stateU
	stateHigh
	stateHighBackslash
	stateHighU
	stateClosed
)

func NewDecoder() *Decoder {
	return &Decoder{}
}

func (d *Decoder) Write(p []byte) (int, error) {
	for i, b := range p {
		at := d.offset + i
		d.checked++
		switch d.state {
		case stateOpen:
			if b != '"' {
				return i, d.fail(ErrClose, at)
			}
			d.state = stateValue
		case stateValue:
			if err := d.valueByte(b, at); err != nil {
				return i, err
			}
		case stateEscape:
			d.escapeByte(b, at)
		case stateU, stateHighU:
			if err := d.hexByte(b, at); err != nil {
				return i, err
			}
		case stateHigh:
			if b != '\\' {
				return i, d.fail(ErrSurrogate, d.highStart)
			}
			d.state = stateHighBackslash
		case stateHighBackslash:
			if b != 'u' {
				if esc.Classify(b) == esc.Simple {
					return i, d.fail(ErrSurrogate, d.highStart)
				}
				return i, d.fail(ErrEscape, at)
			}
			d.state, d.hexN, d.hex = stateHighU, 0, 0
		case stateClosed:
			return i, d.fail(ErrTrailing, at)
		}
		if d.err != nil {
			return i, d.err
		}
	}
	d.offset += len(p)
	return len(p), nil
}

func (d *Decoder) Close() error {
	if d.err != nil {
		return d.err
	}
	switch d.state {
	case stateClosed:
		return nil
	case stateEscape, stateHighBackslash:
		return d.fail(ErrEscape, d.offset)
	case stateU:
		return d.fail(ErrHex, d.offset-d.hexN-2)
	case stateHigh:
		return d.fail(ErrSurrogate, d.highStart)
	case stateHighU:
		return d.fail(ErrHex, d.highStart+6)
	default:
		return d.fail(ErrClose, d.offset)
	}
}

func (d *Decoder) String() string {
	return string(d.dst)
}

func (d *Decoder) CheckedBytes() int {
	return d.checked
}

func (d *Decoder) fail(sentinel error, offset int) error {
	err := sentinel
	if syntax, ok := err.(SyntaxError); ok {
		syntax.Offset = offset
		err = syntax
	}
	if invalid, ok := err.(InvalidUTF8Error); ok {
		invalid.Offset = offset
		err = invalid
	}
	d.err = err
	d.state = stateClosed
	return err
}

func (d *Decoder) valueByte(b byte, at int) error {
	if b == '"' {
		d.state = stateClosed
		return nil
	}
	if b == '\\' {
		d.state, d.raw, d.want = stateEscape, nil, 0
		return nil
	}
	if b < 0x20 {
		return d.fail(ErrControl, at)
	}
	d.raw = append(d.raw, b)
	if len(d.raw) == 1 {
		d.want = firstByteLen(b)
	}
	if d.want == 0 {
		return d.fail(InvalidUTF8Error{}, at)
	}
	if len(d.raw) == d.want {
		r, _ := utf8.DecodeRune(d.raw)
		if r == utf8.RuneError {
			return d.fail(InvalidUTF8Error{}, at-len(d.raw)+1)
		}
		d.dst, d.raw, d.want = append(d.dst, d.raw...), nil, 0
	}
	return nil
}

func firstByteLen(b byte) int {
	switch {
	case b < 0x80:
		return 1
	case b < 0xC2 || b > 0xF4:
		return 0
	case b < 0xE0:
		return 2
	case b < 0xF0:
		return 3
	default:
		return 4
	}
}

func (d *Decoder) escapeByte(b byte, at int) {
	switch esc.Classify(b) {
	case esc.Simple:
		r, _ := esc.SimpleValue(b)
		d.dst = appendRune(d.dst, r)
		d.state = stateValue
	case esc.Unicode:
		d.state, d.hexN, d.hex = stateU, 0, 0
	default:
		d.err = d.fail(ErrEscape, at)
	}
}

func (d *Decoder) hexByte(b byte, at int) error {
	v, ok := esc.HexValue(b)
	start := at - d.hexN - 1
	if !ok {
		return d.fail(ErrHex, start)
	}
	d.hex, d.hexN = d.hex<<4+rune(v), d.hexN+1
	if d.hexN < 4 {
		return nil
	}
	if d.state == stateHighU {
		return d.completeSecond(start)
	}
	return d.completeUnicode(start)
}

func (d *Decoder) completeUnicode(start int) error {
	switch {
	case esc.IsHighSurrogate(d.hex):
		d.high, d.highStart, d.state = d.hex, start, stateHigh
	case esc.IsLowSurrogate(d.hex):
		return d.fail(ErrSurrogate, start)
	default:
		d.dst = appendRune(d.dst, d.hex)
		d.state = stateValue
	}
	return nil
}

func (d *Decoder) completeSecond(start int) error {
	if r, ok := esc.SurrogatePair(d.high, d.hex); ok {
		d.dst = appendRune(d.dst, r)
		d.state = stateValue
	} else {
		return d.fail(ErrSurrogate, start)
	}
	return nil
}

func appendRune(out []byte, r rune) []byte {
	return append(out, string(r)...)
}
