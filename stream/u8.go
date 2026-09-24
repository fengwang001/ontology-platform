package stream

import (
	"ontology/scalar"
	"ontology/u8"
)

// feed 返回 stop 与 p 中本次消耗的字节数（0 表示 p[0] 留作重放）。
// 不变量：每个输入字节只检查一次（hold 重放不增加 Checks）。
func (t *Transcoder) feed(p []byte) (bool, int) {
	if t.bom == bomUnknown && !t.cfg.NoLeadingBOM {
		return t.feedBOM(p)
	}
	if t.cfg.Dir <= U8toU16BE {
		return t.feedU8(p)
	}
	return t.feedU16(p)
}

func (t *Transcoder) feedU8(p []byte) (bool, int) {
	c := p[0]
	t.st.Checks++
	if len(t.hold) == 0 {
		switch {
		case c < 0x80:
			return t.resolve(rune(c), 0, p[1:], 1)
		case u8.IsCont(c) || c < 0xC2 || c > 0xF4:
			return t.resolveBad(0, p[1:], 1)
		}
		t.hold = append(t.hold, c) // 未结案，不计 Consumed
		return false, 1
	}
	idx := len(t.hold)
	n, lo, hi := leadSpec8(t.hold[0])
	if idx == 1 {
		if !u8.IsCont(c) || c < lo || c > hi {
			t.hold = t.hold[:0]
			return t.resolveBad(1, p, 0) // 吞掉 hold 中的首字节；c 重放
		}
		t.hold = append(t.hold, c)
		if n == 2 {
			return t.finishPrefix(p[1:], 1)
		}
		return false, 1
	}
	if !u8.IsCont(c) {
		h := len(t.hold)
		t.hold = t.hold[:0]
		return t.resolveBad(h, p, 0) // 合法前缀断开：吞掉 hold；c 重放
	}
	t.hold = append(t.hold, c)
	if len(t.hold) == n {
		return t.finishPrefix(p[1:], 1)
	}
	return false, 1
}

func (t *Transcoder) finishPrefix(rest []byte, adv int) (bool, int) {
	r := decodeHeld(t.hold)
	h := len(t.hold)
	if !scalar.IsScalar(r) {
		return t.resolveBad(h, rest, adv)
	}
	return t.resolve(r, h, rest, adv)
}

func decodeHeld(b []byte) rune {
	r := rune(b[0] & (0xFF >> uint(len(b)+1)))
	for i := 1; i < len(b); i++ {
		r = r<<6 | rune(b[i]&0x3F)
	}
	return r
}

// resolve 结案一个合法标量：held 字节来自 hold，adv 字节来自本次 p。
func (t *Transcoder) resolve(r rune, held int, rest []byte, adv int) (bool, int) {
	if e := t.emit(r); e != nil {
		return true, 0 // 上限：不清 hold、不消费
	}
	t.hold = t.hold[:0]
	t.st.Scalars++
	t.st.Consumed += int64(held + adv)
	t.abs += int64(held + adv)
	return false, adv
}

// resolveBad 结案一个非法单元（总吞掉 size = held+adv 字节）。
func (t *Transcoder) resolveBad(held int, rest []byte, adv int) (bool, int) {
	size := held + adv
	if t.cfg.Strict {
		t.hold = t.hold[:0]
		t.fail(&Error{Err: ErrIllegal, Offset: t.abs, Len: size})
		return true, 0
	}
	if e := t.emit(scalar.Replacement); e != nil {
		return true, 0
	}
	t.hold = t.hold[:0]
	t.st.BadUnits++
	t.st.BadBytes += int64(size)
	t.st.Consumed += int64(size)
	t.abs += int64(size)
	return false, adv
}

func leadSpec8(c byte) (n int, lo, hi byte) {
	switch {
	case c <= 0xDF:
		return 2, 0x80, 0xBF
	case c == 0xE0:
		return 3, 0xA0, 0xBF
	case c <= 0xEC:
		return 3, 0x80, 0xBF
	case c == 0xED:
		return 3, 0x80, 0x9F
	case c <= 0xEF:
		return 3, 0x80, 0xBF
	case c == 0xF0:
		return 4, 0x90, 0xBF
	case c <= 0xF3:
		return 4, 0x80, 0xBF
	}
	return 4, 0x80, 0x8F
}
