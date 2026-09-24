package stream

import (
	"ontology/scalar"
)

// UTF-16 状态复用 hold：
//   len(hold)==1：半个 code unit；
//   len(hold)==2 且 pendHi>=0：一个待配对的当前 code unit（重放用）；
//   len(hold)==3：待配高代理 2 字节 + 半个 code unit。

func (t *Transcoder) feedU16(p []byte) (bool, int) {
	// 有待重放的 code unit
	if t.pendHi >= 0 && len(t.hold) >= 2 {
		u := t.cu(t.hold[len(t.hold)-2], t.hold[len(t.hold)-1])
		t.hold = t.hold[:0]
		return t.cuPairOrLone(u, p, 0)
	}
	var a, b byte
	var adv int
	if len(t.hold) == 1 {
		t.st.Checks++
		a, b, adv = t.hold[0], p[0], 1
		t.hold = t.hold[:0]
	} else {
		if len(p) < 2 {
			t.st.Checks++
			t.hold = append(t.hold, p[0])
			return false, 1
		}
		t.st.Checks += 2
		a, b, adv = p[0], p[1], 2
	}
	return t.cuPairOrLone(t.cu(a, b), p[adv:], adv)
}

// cuPairOrLone 处理 code unit u；其原始字节为 rest 之前的 adv 个字节。
func (t *Transcoder) cuPairOrLone(u uint16, rest []byte, adv int) (bool, int) {
	r := rune(u)
	switch {
	case t.pendHi >= 0 && scalar.IsLowSurrogate(r):
		pr, _ := scalar.SurrogatePair(t.pendHi, r)
		t.pendHi = -1
		return t.scalarCU(pr, rest, adv, true)
	case t.pendHi >= 0:
		off := t.pendOff
		t.pendHi = -1
		if t.cfg.Strict {
			t.fail(&Error{Err: ErrIllegal, Offset: off, Len: 2})
			return true, 0
		}
		if e := t.emit(scalar.Replacement); e != nil {
			t.pendHi = -1
			return true, 0
		}
		t.st.BadUnits++
		t.st.BadBytes += 2
		t.st.Consumed += 2
		t.abs += 2
		// 当前 code unit 不属于高代理单元：缓存原始字节后重放
		t.hold = append(t.hold, cuBytes(t.cfg.Dir, u)...)
		return false, adv
	case scalar.IsHighSurrogate(r):
		t.pendHi = r
		t.pendOff = t.abs
		return false, adv
	case scalar.IsLowSurrogate(r):
		if t.cfg.Strict {
			t.fail(&Error{Err: ErrIllegal, Offset: t.abs, Len: 2})
			return true, 0
		}
		if e := t.emit(scalar.Replacement); e != nil {
			return true, 0
		}
		t.st.BadUnits++
		t.st.BadBytes += 2
		t.st.Consumed += 2
		t.abs += 2
		return false, adv
	}
	return t.scalarCU(r, rest, adv, false)
}

func (t *Transcoder) scalarCU(r rune, rest []byte, adv int, pair bool) (bool, int) {
	if e := t.emit(r); e != nil {
		return true, 0
	}
	t.st.Scalars++
	n := 2
	if pair {
		n = 4
	}
	t.st.Consumed += int64(n)
	t.abs += int64(n)
	return false, adv
}

func cuBytes(dir Dir, u uint16) []byte {
	if dir == U16LEtoU8 {
		return []byte{byte(u), byte(u >> 8)}
	}
	return []byte{byte(u >> 8), byte(u)}
}

func (t *Transcoder) cu(a, b byte) uint16 {
	if t.cfg.Dir == U16LEtoU8 {
		return uint16(a) | uint16(b)<<8
	}
	return uint16(a)<<8 | uint16(b)
}
