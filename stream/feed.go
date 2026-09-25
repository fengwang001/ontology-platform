package stream

import (
	"ontology/scalar"
	"ontology/u16"
	"ontology/u8"
)

func (t *Transcoder) step(b byte) bool {
	if !t.is16In() {
		return t.feed8(b)
	}
	return t.feed16(b)
}

func (t *Transcoder) replay(h []byte) {
	for _, b := range h {
		for {
			if adv := t.step(b); adv || t.terr != nil {
				break
			}
		}
		if t.terr != nil {
			return
		}
	}
}

// feedHead 收集流首 BOM；未命中则把已收前缀重放给正常机。
func (t *Transcoder) feedHead(b byte) bool {
	t.head = append(t.head, b)
	h := len(t.head)
	switch {
	case t.is16In() && h == 1:
		return true
	case t.is16In():
		if o, ok := u16.DetectOrder(t.head); ok {
			t.order, t.headDone, t.st.BOMBytes = o, true, t.st.BOMBytes+2
			if t.cfg.KeepBOM {
				t.scalar(scalar.BOM, 2)
			}
			return true
		}
	case h < 3:
		if h == 1 {
			return b == 0xEF // 只有 EF 可能继续 BOM
		}
		return t.head[0] == 0xEF && t.head[1] == 0xBB
	case t.head[0] == 0xEF && t.head[1] == 0xBB && b == 0xBF:
		t.headDone, t.st.BOMBytes = true, t.st.BOMBytes+3
		if t.cfg.KeepBOM {
			t.scalar(scalar.BOM, 3)
		}
		return true
	}
	h = len(t.head)
	t.head, t.headDone = t.head[:0], true
	t.replay(append([]byte(nil), t.head[:h]...))
	return true
}

// feed8 是 UTF-8 增量机；false 表示当前字节未消费，须重解析。
func (t *Transcoder) feed8(b byte) bool {
	if len(t.pend) > 0 {
		return t.feed8Cont(b)
	}
	switch n := scalar.LeadLen(b); {
	case n == 0:
		t.badUnit(t.baseOff+t.consumed, 1)
	case n == 1:
		t.scalar(rune(b), 1)
	default:
		t.pend, t.pendStart = append(t.pend, b), t.baseOff+t.consumed
	}
	return true
}

func (t *Transcoder) feed8Cont(b byte) bool {
	l, lead, want := len(t.pend), t.pend[0], scalar.LeadLen(t.pend[0])
	ok := true
	if l == 1 {
		ok = scalar.SecondOK(lead, b) // 内含 80..BF 与 E0/ED/F0/F4 区间约束
	} else if !scalar.IsContinuation(b) {
		ok = false
	}
	if !ok {
		start, n := t.pendStart, l+1
		t.pend = append(t.pend, b)
		t.pend = t.pend[:0]
		t.badUnit(start, n)
		return false
	}
	t.pend = append(t.pend, b)
	if len(t.pend) == want {
		u, p := u8.First(t.pend), t.pend
		t.pend = t.pend[:0]
		t.scalar(u.R, len(p))
	}
	return true
}

func (t *Transcoder) badUnit(off int64, l int) {
	if t.cfg.Strict {
		t.fail(ErrBadUnit, off, l)
		return
	}
	if t.emit(scalar.Replacement) {
		t.st.Bad, t.st.BadBytes, t.consumed = t.st.Bad+1, t.st.BadBytes+int64(l), t.consumed+int64(l)
	}
}

func (t *Transcoder) scalar(r rune, inLen int) {
	if t.emit(r) {
		t.st.Scalars, t.st.InBytes, t.consumed = t.st.Scalars+1, t.st.InBytes+int64(inLen), t.consumed+int64(inLen)
	}
}

// emit 编码一个标量；越过 MaxOut 时在标量边界停下并置 ErrLimit。
func (t *Transcoder) emit(r rune) bool {
	n := u8.EncLen(r)
	if !t.is16In() && t.cfg.Dir != U8ToU8 {
		n = u16.EncLen(r)
	}
	if t.cfg.MaxOut > 0 && len(t.out)+n > t.cfg.MaxOut {
		t.fail(ErrLimit, 0, 0)
		return false
	}
	if t.is16In() || t.cfg.Dir == U8ToU8 {
		t.out = u8.Encode(t.out, r)
	} else {
		o := u16.LE
		if t.cfg.Dir == U8To16BE {
			o = u16.BE
		}
		t.out = u16.Encode(t.out, r, o)
	}
	return true
}
