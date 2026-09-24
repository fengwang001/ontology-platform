package jstr

import "ontology/esc"

const (
	sStart = iota
	sText
	sCont // incomplete multibyte rune
	sEsc  // saw backslash
	sHex  // \u unit; hpos -1/-2 expect the low half's backslash/u
	sEnd
)

// step processes one freshly arrived physical byte. A byte reaches step once,
// so Decoder.Examined never exceeds the physical input length.
func (d *Decoder) step(b byte) error {
	off := d.off
	d.off++
	switch d.state {
	case sStart:
		if b != '"' {
			return d.fail(ErrNotString, off)
		}
		d.state = sText
	case sText:
		d.text(b, off)
	case sCont:
		if badCont(byte(d.lead), d.got, b) {
			return d.fail(ErrInvalidUTF8, d.utfAt)
		}
		d.pbuf = append(d.pbuf, b)
		if d.got++; d.got == d.need {
			d.out = append(d.out, d.pbuf...)
			d.pbuf, d.state = d.pbuf[:0], sText
		}
	case sEsc:
		switch {
		case esc.IsSimple(b):
			d.out = append(d.out, byte(esc.DecodeSimple(b)))
			d.state = sText
		case b == 'u':
			d.hpos, d.hval, d.high = 0, 0, 0
			d.state = sHex
		default:
			return d.fail(ErrUnknownEscape, d.escAt)
		}
	case sHex:
		switch d.hpos {
		case -1:
			if b != '\\' {
				return d.fail(ErrSurrogate, d.highAt)
			}
			d.hpos = -2
		case -2:
			if b != 'u' {
				return d.fail(ErrSurrogate, d.highAt)
			}
			d.hpos, d.hval = 0, 0
		default:
			v := esc.HexValue(b)
			if v < 0 {
				if d.high != 0 {
					return d.fail(ErrSurrogate, d.highAt)
				}
				return d.fail(ErrBadHex, d.escAt)
			}
			d.hval = d.hval<<4 | rune(v)
			if d.hpos++; d.hpos == 4 {
				d.completeUnit()
			}
		}
	case sEnd:
		return d.fail(ErrTrailingBytes, off)
	}
	return d.err
}

func (d *Decoder) text(b byte, off int) {
	switch {
	case b == '"':
		d.state = sEnd
	case b == '\\':
		d.escAt, d.state = off, sEsc
	case b < 0x20:
		d.fail(ErrControl, off)
	case b < 0x80:
		d.out = append(d.out, b)
	case b < 0xC2 || b > 0xF4:
		d.fail(ErrInvalidUTF8, off)
	default:
		d.need = 1
		if b >= 0xE0 {
			d.need = 2
		}
		if b >= 0xF0 {
			d.need = 3
		}
		d.lead, d.got, d.utfAt = rune(b), 0, off
		d.pbuf = append(d.pbuf[:0], b)
		d.state = sCont
	}
}

func badCont(lead byte, got int, b byte) bool {
	return b < 0x80 || b > 0xBF || (got == 0 &&
		((lead == 0xE0 && b < 0xA0) || (lead == 0xED && b > 0x9F) ||
			(lead == 0xF0 && b < 0x90) || (lead == 0xF4 && b > 0x8F)))
}

// completeUnit finalizes one assembled \uXXXX unit.
func (d *Decoder) completeUnit() {
	switch {
	case esc.IsHighSurrogate(d.hval):
		if d.high != 0 {
			d.fail(ErrSurrogate, d.highAt)
			return
		}
		d.high, d.highAt, d.hpos = d.hval, d.escAt, -1
	case esc.IsLowSurrogate(d.hval):
		if d.high == 0 {
			d.fail(ErrSurrogate, d.escAt)
			return
		}
		d.emit(esc.Pair(d.high, d.hval))
		d.high, d.state = 0, sText
	default:
		if d.high != 0 {
			d.fail(ErrSurrogate, d.highAt)
			return
		}
		d.emit(d.hval)
		d.state = sText
	}
}
