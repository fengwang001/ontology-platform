package jstr

import (
	"unicode/utf8"

	"ontology/esc"
)

func (d *Decoder) Write(p []byte) (int, error) {
	for i := range p {
		d.pos, d.checks = d.pos+1, d.checks+1
		err := d.step(p[i], d.pos-1)
		if err != nil {
			return i + 1, err
		}
	}
	return len(p), nil
}
func (d *Decoder) step(b byte, o int) error {
	switch d.st {
	case 0:
		if b != '"' {
			return d.fail(KindUnterminated, 0)
		}
		d.st = 1
	case 1:
		return d.raw(b, o)
	case 2:
		return d.esc(b)
	case 3:
		return d.uhex(b)
	case 5:
		return d.fail(KindTrailing, o)
	}
	return nil
}
func (d *Decoder) raw(b byte, o int) error {
	if d.hi >= 0 {
		if b == '\\' {
			d.escOff, d.st = o, 2
			return nil
		}
		return d.fail(KindUnicode, d.uOff)
	}
	switch b {
	case '"':
		return d.flush(true)
	case '\\':
		err := d.flush(false)
		if err != nil {
			return err
		}
		d.escOff, d.st = o, 2
	default:
		if b < 0x20 {
			return d.fail(KindControl, o)
		}
		if len(d.buf) == 0 {
			d.rawStart = o
		}
		d.buf = append(d.buf, b)
	}
	return nil
}
func (d *Decoder) esc(b byte) error {
	switch {
	case unesc[b] != 0 && d.hi < 0:
		d.out.WriteByte(unesc[b])
		d.st = 1
	case b == 'u':
		d.uN, d.uV, d.uOff, d.st = 0, 0, d.escOff, 3
	case d.hi >= 0:
		return d.fail(KindUnicode, d.uOff)
	default:
		return d.fail(KindEscape, d.escOff)
	}
	return nil
}
func (d *Decoder) uhex(b byte) error {
	v, ok := esc.HexVal(b)
	if !ok {
		return d.fail(KindUnicode, d.uOff)
	}
	d.uV, d.uN = d.uV<<4|v, d.uN+1
	if d.uN == 4 {
		return d.unit()
	}
	return nil
}
func (d *Decoder) unit() error {
	switch r := d.uV; {
	case d.hi >= 0 && esc.IsLowSurrogate(r):
		d.out.WriteRune(esc.SurrogatePair(d.hi, r))
		d.hi = -1
	case d.hi >= 0:
		return d.fail(KindUnicode, d.uOff)
	case esc.IsHighSurrogate(r):
		d.hi = r
	case esc.IsLowSurrogate(r):
		return d.fail(KindUnicode, d.uOff)
	default:
		d.out.WriteRune(r)
	}
	d.st = 1
	return nil
}
func (d *Decoder) flush(closing bool) error {
	if len(d.buf) > 0 && !utf8.Valid(d.buf) {
		return d.fail(KindUTF8, d.rawStart+badOff(d.buf))
	}
	d.out.Write(d.buf)
	d.buf = d.buf[:0]
	if closing {
		d.st = 5
	}
	return nil
}
func badOff(p []byte) int {
	for i := 0; i < len(p); {
		r, n := utf8.DecodeRune(p[i:])
		if r == utf8.RuneError && n == 1 {
			return i
		}
		i += n
	}
	return 0
}

func (d *Decoder) Close() (string, error) {
	if d.err != nil {
		return "", d.err
	}
	switch {
	case d.st == 5:
		return d.out.String(), nil
	case d.st == 2:
		return "", d.fail(KindEscape, d.escOff)
	case d.st == 3:
		return "", d.fail(KindUnicode, d.uOff)
	case d.st == 1 && d.hi >= 0:
		return "", d.fail(KindUnicode, d.uOff)
	case d.st == 1:
		if !utf8.Valid(d.buf) {
			return "", d.fail(KindUTF8, d.rawStart+badOff(d.buf))
		}
		return "", d.fail(KindUnterminated, d.pos)
	default:
		return "", d.fail(KindUnterminated, d.pos)
	}
}
