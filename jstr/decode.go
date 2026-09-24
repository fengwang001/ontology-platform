package jstr

import (
	"unicode/utf8"

	"ontology/esc"
)

const (
	sStart uint8 = iota
	sText
	sEsc
	sUni
	sRaw
	sDone
)

// Write feeds a prefix of the literal. A returned error is sticky.
func (d *Decoder) Write(p []byte) (int, error) {
	if d.err != nil {
		return 0, d.err
	}
	for i, b := range p {
		at := d.pos + i
		d.checked++
		if err := d.step(b, at); err != nil {
			d.pos += i
			d.err = err
			return i, err
		}
	}
	d.pos += len(p)
	return len(p), nil
}

// Close verifies that the complete literal has been supplied.
func (d *Decoder) Close() error {
	if d.err != nil {
		return d.err
	}
	switch d.state {
	case sDone:
	case sRaw:
		d.err = d.fail(ErrInvalidUTF8, d.start)
	case sEsc:
		d.err = d.fail(ErrEscape, d.start)
	case sUni:
		d.err = d.fail(ErrUnicode, d.start)
	default:
		if d.highSet {
			d.err = d.fail(ErrSurrogate, int(d.highAt))
		} else {
			d.err = d.fail(ErrMissing, d.pos)
		}
	}
	return d.err
}

func (d *Decoder) step(b byte, at int) error {
	switch d.state {
	case sStart:
		if b != '"' {
			return d.fail(ErrMissing, at)
		}
		d.state = sText
	case sText:
		return d.text(b, at)
	case sEsc:
		return d.esc(b, at)
	case sUni:
		return d.uni(b, at)
	case sRaw:
		return d.raw(b, at)
	default:
		return d.fail(ErrTrailing, at)
	}
	return nil
}

func (d *Decoder) text(b byte, at int) error {
	switch {
	case b == '"':
		if d.highSet {
			return d.fail(ErrSurrogate, int(d.highAt))
		}
		d.state = sDone
	case d.highSet && b != '\\':
		return d.fail(ErrSurrogate, int(d.highAt))
	case b == '\\':
		d.state, d.start = sEsc, at
	case b < 0x20:
		return d.fail(ErrControl, at)
	case b < 0x80:
		d.out = append(d.out, b)
	case b < 0xC2 || b > 0xF4:
		return d.fail(ErrInvalidUTF8, at)
	default:
		d.state, d.start, d.rawN, d.raw[0] = sRaw, at, 0, b
	}
	return nil
}

func (d *Decoder) esc(b byte, at int) error {
	if b == 'u' {
		d.state, d.hexN, d.hexV = sUni, 0, 0
		return nil
	}
	if d.highSet {
		return d.fail(ErrSurrogate, int(d.highAt))
	}
	r, ok := esc.Simple(b)
	if !ok {
		return d.fail(ErrEscape, d.start)
	}
	d.out, d.state = appendRune(d.out, r), sText
	return nil
}

func (d *Decoder) uni(b byte, at int) error {
	h, ok := esc.Hex(b)
	if !ok {
		return d.fail(ErrUnicode, d.start)
	}
	d.hexV, d.hexN = d.hexV<<4|h, d.hexN+1
	if d.hexN < 4 {
		return nil
	}
	d.state = sText
	r := d.hexV
	switch {
	case d.highSet:
		r2, ok := esc.Pair(d.high, r)
		if !ok {
			return d.fail(ErrSurrogate, int(d.highAt))
		}
		d.high, d.highSet = 0, false
		d.out = appendRune(d.out, r2)
	case esc.IsHigh(r):
		d.high, d.highAt, d.highSet = r, rune(d.start), true
	case esc.IsLow(r):
		return d.fail(ErrSurrogate, at-5)
	default:
		d.out = appendRune(d.out, r)
	}
	return nil
}

func (d *Decoder) raw(b byte, at int) error {
	first := d.raw[0]
	want := 2
	if first >= 0xE0 {
		want = 3
	}
	if first >= 0xF0 {
		want = 4
	}
	if b < 0x80 || b > 0xBF || (d.rawN == 0 && !contOK(first, b)) {
		return d.fail(ErrInvalidUTF8, at)
	}
	d.rawN++
	d.raw[d.rawN] = b
	if d.rawN < want-1 {
		return nil
	}
	r, _ := utf8.DecodeRune(d.raw[:want])
	if r == utf8.RuneError {
		return d.fail(ErrInvalidUTF8, d.start)
	}
	d.out, d.state = append(d.out, d.raw[:want]...), sText
	return nil
}

func contOK(first, b byte) bool {
	switch first {
	case 0xE0:
		return b >= 0xA0
	case 0xED:
		return b <= 0x9F
	case 0xF0:
		return b >= 0x90
	case 0xF4:
		return b <= 0x8F
	default:
		return true
	}
}

func (d *Decoder) fail(op error, offset int) *OffsetError { return &OffsetError{op, offset} }

func appendRune(out []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(out, byte(r))
	case r < 0x800:
		return append(out, 0xC0|byte(r>>6), 0x80|byte(r&0x3F))
	case r < 0x10000:
		return append(out, 0xE0|byte(r>>12), 0x80|byte((r>>6)&0x3F), 0x80|byte(r&0x3F))
	default:
		return append(out, 0xF0|byte(r>>18), 0x80|byte((r>>12)&0x3F), 0x80|byte((r>>6)&0x3F), 0x80|byte(r&0x3F))
	}
}
