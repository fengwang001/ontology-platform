package stream

import "ontology/scalar"

func (t *Transcoder) Write(p []byte) (int, error) {
	if t.term != nil {
		return 0, t.term
	}
	start := t.s.Consumed
	var err error
	if t.cfg.From == UTF8 {
		err = t.write8(p)
	} else {
		err = t.write16(p)
	}
	if err != nil {
		t.term = err
	}
	return t.s.Consumed - start, err
}

func (t *Transcoder) write8(p []byte) error {
	i := 0
	for i < len(p) {
		t.s.Checks++
		b := p[i]
		if t.phase != 1 {
			if b < 0x80 {
				err := t.finalize(rune(b), 1, t.bomOpen && b == scalar.BOM)
				i++
				if err != nil {
					return err
				}
				continue
			}
			L, ok := leadLen(b)
			if !ok {
				if err := t.finalize(-1, 1, false); err != nil {
					return err
				}
				i++
				continue
			}
			t.phase, t.want = 1, L
			t.r = rune(b & (0xFF >> uint(L)))
			t.pend = append(t.pend[:0], b)
			i++
			continue
		}
		ok := len(t.pend) > 1 || secondOK(t.pend[0], b)
		if !ok || !isCont(b) {
			n := len(t.pend)
			t.pend, t.phase = t.pend[:0], 0
			if err := t.finalize(-1, n, false); err != nil {
				return err
			}
			continue // b 重新解析
		}
		t.r = (t.r << 6) | rune(b&0x3F)
		t.pend = append(t.pend, b)
		i++
		if len(t.pend) == t.want {
			r, n := t.r, len(t.pend)
			bom := t.bomOpen && r == scalar.BOM
			t.pend, t.phase = t.pend[:0], 0
			if err := t.finalize(r, n, bom); err != nil {
				return err
			}
		}
	}
	return nil
}

type u16unit struct {
	r    rune // -1=非法代码单元
	bom  bool
	high bool
}

func (t *Transcoder) write16(p []byte) error {
	if len(t.pend) > 0 {
		p = append(append([]byte{}, t.pend...), p...)
		t.pend = t.pend[:0]
		t.phase = 0
	}
	for len(p) > 0 {
		if len(p) < 2 {
			t.pend = append(t.pend, p...)
			return nil
		}
		t.s.Checks += 2
		if t.bomOpen {
			big := p[0] == 0xFE && p[1] == 0xFF
			le := p[0] == 0xFF && p[1] == 0xFE
			if big || le {
				t.big = big
				p = p[2:]
				t.bomOpen = false
				if err := t.finalize(scalar.BOM, 2, true); err != nil {
					return err
				}
				continue
			}
			t.bomOpen = false
		}
		u := t.decodeUnit(p)
		if u.high && len(p) < 4 {
			// 高代理可能被切分点切开：缓存这 2 字节等待下一批。
			t.pend = append(t.pend, p[:2]...)
			t.phase = 2
			return nil
		}
		if u.high && len(p) >= 4 {
			low := t.decodeUnit(p[2:])
			if low.r >= 0 && scalar.IsLowSurrogate(uint16(low.r)) {
				err := t.finalize(scalar.SurrogatePair(uint16(u.r),
					uint16(low.r)), 4, false)
				p = p[4:]
				if err != nil {
					return err
				}
				continue
			}
			if err := t.finalize(-1, 2, false); err != nil {
				return err
			}
			p = p[2:] // 第二代码单元重新解析
			continue
		}
		if err := t.finalize(u.r, 2, false); err != nil {
			return err
		}
		p = p[2:]
	}
	return nil
}

func (t *Transcoder) decodeUnit(p []byte) u16unit {
	var u uint16
	if t.big {
		u = uint16(p[0])<<8 | uint16(p[1])
	} else {
		u = uint16(p[1])<<8 | uint16(p[0])
	}
	if t.bomOpen && u == scalar.BOM {
		return u16unit{r: rune(u), bom: true}
	}
	switch {
	case scalar.IsHighSurrogate(u):
		return u16unit{r: rune(u), high: true}
	case scalar.IsLowSurrogate(u):
		return u16unit{r: -1}
	default:
		return u16unit{r: rune(u)}
	}
}
