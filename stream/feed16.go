package stream

import "ontology/scalar"

func (t *Transcoder) cu(p []byte) uint16 {
	u := uint16(p[0]) | uint16(p[1])<<8
	if t.order == 2 { // u16.BE
		u = uint16(p[0])<<8 | uint16(p[1])
	}
	return u
}

// feed16 是 UTF-16 增量机；false 表示当前码元未消费，须重解析。
func (t *Transcoder) feed16(b byte) bool {
	t.pend = append(t.pend, b)
	l := len(t.pend)
	if (!t.hi && l%2 == 1) || (t.hi && l == 3) {
		if l == 1 {
			t.pendStart = t.baseOff + t.consumed
		}
		return true
	}
	if t.hi {
		return t.feed16Pair()
	}
	return t.feed16Unit()
}

func (t *Transcoder) feed16Pair() bool {
	hi, lo, start := t.cu(t.pend[:2]), t.cu(t.pend[2:4]), t.pendStart
	t.hi, t.pend = false, t.pend[:0]
	if scalar.IsLowSurrogate(lo) {
		t.scalar(scalar.FromSurrogatePair(hi, lo), 4)
		return true
	}
	t.badUnit(start, 2)
	return false
}

func (t *Transcoder) feed16Unit() bool {
	u, start := t.cu(t.pend[:2]), t.pendStart
	t.pend = t.pend[:0]
	switch {
	case scalar.IsHighSurrogate(u):
		t.hi, t.pendStart = true, start
		if t.order == 2 {
			t.pend = append(t.pend, byte(u>>8), byte(u))
		} else {
			t.pend = append(t.pend, byte(u), byte(u>>8))
		}
	case scalar.IsLowSurrogate(u):
		t.badUnit(start, 2)
	default:
		t.scalar(rune(u), 2)
	}
	return true
}
