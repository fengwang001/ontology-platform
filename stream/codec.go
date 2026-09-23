package stream

import (
	"ontology/u16"
	"ontology/u8"
)

func (t *Transcoder) pendingLen() int {
	if t.cfg.Dir <= U8toU16BE {
		return t.d8.PendingLen()
	}
	return t.d16.PendingLen()
}

func (t *Transcoder) track(b byte) {
	if t.pendingLen() > 0 {
		if len(t.pend) == 0 {
			t.pendStart = t.consumed
		}
		t.pend = append(t.pend, b)
		if int64(len(t.pend)) > t.maxPend {
			t.maxPend = int64(len(t.pend))
		}
	} else {
		t.pend = t.pend[:0]
	}
}

func (t *Transcoder) feed(b byte) error {
	if t.cfg.Dir <= U8toU16BE {
		r := t.d8.Feed(b)
		t.track(b)
		switch r.Event {
		case u8.EvNeed:
			return nil
		case u8.EvOK:
			off := t.consumed - int64(r.UnitLen)
			if r.Rune == 0xFEFF && off == 0 && t.scalars == 0 && t.badUnits == 0 && t.bom == 0 {
				t.emitBOM(r.UnitLen)
				return nil
			}
			return t.commitScalar(r.Rune, r.UnitLen, off)
		default:
			off := t.consumed - int64(r.UnitLen)
			if r.Reprocess {
				t.rp = append(t.rp, b)
			}
			return t.commitBad(r.UnitLen, off)
		}
	}
	r := t.d16.Feed(b)
	if len(r.RP) > 0 {
		t.rp = append(t.rp, r.RP...)
	}
	t.track(b)
	switch r.Event {
	case u16.EvNeed:
		return nil
	case u16.EvOK:
		return t.commitScalar(r.Rune, r.UnitLen, t.consumed-int64(r.UnitLen))
	case u16.EvBOM:
		if t.cfg.Dir == U16AutoToU8 {
			t.autoOrder = u16.LE
			if r.B0 == 0xFE && r.B1 == 0xFF {
				t.autoOrder = u16.BE
			}
			t.d16.SetOrder(t.autoOrder)
		}
		t.emitBOM(2)
		return nil
	default:
		return t.commitBad(r.UnitLen, t.consumed-int64(r.UnitLen))
	}
}

func (t *Transcoder) accepted(off int64) bool {
	if off < int64(t.cfg.SkipBefore) {
		return false
	}
	return t.cfg.StopAt <= 0 || off < int64(t.cfg.StopAt)
}

func (t *Transcoder) enc(r rune) int {
	if t.cfg.Dir <= U8toU16BE {
		return u16.Encode(t.buf[:], r, t.order())
	}
	return u8.Encode(t.buf[:], r)
}

func (t *Transcoder) commitScalar(r rune, n int, off int64) error {
	if !t.accepted(off) {
		t.consumed += int64(n)
		return nil
	}
	l := t.enc(r)
	if t.cfg.Limit > 0 && len(t.out)+l > t.cfg.Limit {
		t.fatal = &LimitError{Offset: off}
		return t.fatal
	}
	t.out = append(t.out, t.buf[:l]...)
	t.scalars++
	t.consumed += int64(n)
	return nil
}

func (t *Transcoder) commitBad(n int, off int64) error {
	if t.accepted(off) {
		if t.cfg.Strict {
			t.fatal = &BadError{Offset: off, Length: int64(n)}
			return t.fatal
		}
		l := t.enc(0xFFFD)
		if t.cfg.Limit > 0 && len(t.out)+l > t.cfg.Limit {
			t.fatal = &LimitError{Offset: off}
			return t.fatal
		}
		t.out = append(t.out, t.buf[:l]...)
		t.badUnits++
	}
	t.badBytes += int64(n)
	t.consumed += int64(n)
	return nil
}

func (t *Transcoder) emitBOM(n int) {
	if t.cfg.EmitBOM {
		l := t.enc(0xFEFF)
		t.out = append(t.out, t.buf[:l]...)
	}
	t.bom += int64(n)
	t.consumed += int64(n)
}
