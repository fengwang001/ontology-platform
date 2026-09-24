package jstr

import (
	"errors"
	"unicode/utf8"

	"ontology/esc"
)

var (
	ErrMissingOpeningQuote = errors.New("missing opening quote")
	ErrControlCharacter    = errors.New("unescaped control character")
	ErrInvalidEscape       = errors.New("invalid escape sequence")
	ErrInvalidUnicode      = errors.New("invalid unicode escape")
	ErrMissingClosingQuote = errors.New("missing closing quote")
	ErrTrailingBytes       = errors.New("bytes after closing quote")
	ErrInvalidUTF8         = errors.New("invalid UTF-8")
)

type OffsetError struct {
	Op     error
	Offset int
}

func (e *OffsetError) Error() string { return e.Op.Error() }
func (e *OffsetError) Unwrap() error { return e.Op }

type Decoder struct {
	out       []byte
	phase     int
	offset    int
	token     int
	hexN      int
	hex       [4]byte
	high      rune
	highStart int
	utfNeed   int
	utfSeen   int
	utfStart  int
	utfMin    rune
	checked   int
}

const (
	phaseOpen = iota
	phaseText
	phaseEscape
	phaseUnicode
	phaseClosed
)

func NewDecoder() *Decoder { return &Decoder{} }

func Decode(lit []byte) (string, error) {
	d := NewDecoder()
	if _, err := d.Write(lit); err != nil {
		return "", err
	}
	if err := d.Close(); err != nil {
		return "", err
	}
	return string(d.out), nil
}

func (d *Decoder) Write(p []byte) (int, error) {
	for n, c := range p {
		d.checked++
		off := d.offset
		d.offset++
		switch d.phase {
		case phaseClosed:
			return n, &OffsetError{ErrTrailingBytes, off}
		case phaseOpen:
			if c != '"' {
				return n, &OffsetError{ErrMissingOpeningQuote, off}
			}
			d.phase = phaseText
		case phaseEscape:
			if err := d.escape(c, off); err != nil {
				return n, err
			}
		case phaseUnicode:
			if err := d.unicode(c, off); err != nil {
				return n, err
			}
		default:
			if err := d.text(c, off); err != nil {
				return n, err
			}
		}
	}
	return len(p), nil
}

func (d *Decoder) text(c byte, off int) error {
	if d.utfNeed > 0 {
		return d.utfContinue(c)
	}
	if c == '"' {
		if d.highStart != 0 {
			return &OffsetError{ErrInvalidUnicode, off}
		}
		d.phase = phaseClosed
		return nil
	}
	if c == '\\' {
		if d.highStart != 0 {
			d.token = off
		}
		d.phase = phaseEscape
		return nil
	}
	if c < 0x20 {
		return &OffsetError{ErrControlCharacter, off}
	}
	if c < 0x80 {
		if d.highStart != 0 {
			return &OffsetError{ErrInvalidUnicode, off}
		}
		d.out = append(d.out, c)
		return nil
	}
	if d.highStart != 0 {
		return &OffsetError{ErrInvalidUnicode, off}
	}
	need, min := utf8Info(c)
	if need == 0 {
		return &OffsetError{ErrInvalidUTF8, off}
	}
	d.utfNeed, d.utfSeen, d.utfStart, d.utfMin = need, 1, off, min
	d.out = append(d.out, c)
	return nil
}

func (d *Decoder) utfContinue(c byte) error {
	d.checked++
	if c < 0x80 || c > 0xBF || (d.utfSeen == 1 && rune(c&0x3F) < d.utfMin) {
		return &OffsetError{ErrInvalidUTF8, d.utfStart}
	}
	d.out = append(d.out, c)
	d.utfSeen++
	if d.utfSeen == d.utfNeed {
		d.utfNeed, d.utfMin = 0, 0
	}
	return nil
}

func (d *Decoder) escape(c byte, off int) error {
	if c != 'u' {
		decoded, ok := esc.SimpleEscape(c)
		if !ok {
			return &OffsetError{ErrInvalidEscape, off - 1}
		}
		if d.highStart != 0 {
			return &OffsetError{ErrInvalidUnicode, d.token}
		}
		d.out = append(d.out, decoded)
		d.phase = phaseText
		return nil
	}
	d.hexN, d.token = 0, off-1
	d.phase = phaseUnicode
	return nil
}

func (d *Decoder) unicode(c byte, off int) error {
	value := esc.HexValue(c)
	if value < 0 {
		return &OffsetError{ErrInvalidUnicode, d.token}
	}
	d.hex[d.hexN] = c
	d.hexN++
	if d.hexN < 4 {
		return nil
	}
	code, _ := esc.HexCode(d.hex[:])
	switch {
	case esc.IsHighSurrogate(code):
		if d.highStart != 0 {
			return &OffsetError{ErrInvalidUnicode, d.token}
		}
		d.high, d.highStart = code, d.token
	case esc.IsLowSurrogate(code):
		if d.highStart == 0 {
			return &OffsetError{ErrInvalidUnicode, d.token}
		}
		combined, _ := esc.SurrogatePair(d.high, code)
		d.appendRune(combined)
		d.highStart = 0
	case d.highStart != 0:
		return &OffsetError{ErrInvalidUnicode, d.token}
	default:
		d.appendRune(code)
	}
	d.phase = phaseText
	return nil
}

func (d *Decoder) Close() error {
	switch {
	case d.phase == phaseClosed:
		return nil
	case d.utfNeed > 0:
		return &OffsetError{ErrInvalidUTF8, d.utfStart}
	case d.phase == phaseEscape:
		return &OffsetError{ErrInvalidEscape, d.offset - 1}
	case d.phase == phaseUnicode:
		return &OffsetError{ErrInvalidUnicode, d.token}
	case d.highStart != 0:
		return &OffsetError{ErrInvalidUnicode, d.highStart}
	default:
		return &OffsetError{ErrMissingClosingQuote, d.offset}
	}
}

func (d *Decoder) Bytes() []byte { return d.out }
func (d *Decoder) Checks() int  { return d.checked }

func (d *Decoder) appendRune(r rune) {
	var buf [4]byte
	d.out = append(d.out, buf[:utf8.EncodeRune(buf[:], r)]...)
}

func utf8Info(first byte) (int, rune) {
	switch {
	case first < 0xC2:
		return 0, 0
	case first < 0xE0:
		return 2, 0
	case first == 0xE0:
		return 3, 0x20
	case first < 0xF0:
		return 3, 0
	case first == 0xF0:
		return 4, 0x10
	case first < 0xF5:
		return 4, 0
	default:
		return 0, 0
	}
}

func Encode(s string) ([]byte, error) {
	if !utf8.ValidString(s) {
		return nil, ErrInvalidUTF8
	}
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r < 0x20 {
			out = appendControl(out, r)
		} else {
			if r == '"' || r == '\\' {
				out = append(out, '\\')
			}
			out = append(out, s[i:i+size]...)
		}
		i += size
	}
	return append(out, '"'), nil
}

func appendControl(out []byte, r rune) []byte {
	switch r {
	case '\b':
		return append(out, '\\', 'b')
	case '\f':
		return append(out, '\\', 'f')
	case '\n':
		return append(out, '\\', 'n')
	case '\r':
		return append(out, '\\', 'r')
	case '\t':
		return append(out, '\\', 't')
	}
	const hex = "0123456789abcdef"
	return append(out, '\\', 'u', '0', '0', hex[r>>4], hex[r&0x1f])
}
