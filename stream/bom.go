package stream

import (
	"ontology/scalar"
	"ontology/u16"
	"ontology/u8"
)

// bomPattern 返回流首 BOM 的原始字节模式。
func (t *Transcoder) bomPattern() []byte {
	switch t.cfg.Dir {
	case U8toU16LE, U8toU16BE:
		return []byte{0xEF, 0xBB, 0xBF}
	case U16LEtoU8:
		return u16.BOMLE
	default:
		return u16.BOMBE
	}
}

// feedBOM 只在流首运行；任何不匹配立即转入正常解析（本次喂入字节全部保留）。
func (t *Transcoder) feedBOM(p []byte) (bool, int) {
	pat := t.bomPattern()
	got := len(t.hold)
	if got+len(p) < len(pat) {
		for k := got; k < got+len(p); k++ {
			t.st.Checks++
			if p[k-got] != pat[k] {
				return t.bomReject(p)
			}
		}
		t.hold = append(t.hold, p...)
		return true, len(p) // 等待更多字节
	}
	for k := got; k < len(pat); k++ {
		t.st.Checks++
		if p[k-got] != pat[k] {
			return t.bomReject(p)
		}
	}
	adv := len(pat) - got
	t.hold = t.hold[:0]
	t.bom = bomHandled
	t.st.BOMBytes += int64(len(pat))
	t.st.Consumed += int64(len(pat))
	t.abs += int64(len(pat))
	if t.cfg.EmitBOM {
		if e := t.emit(scalar.BOM); e != nil {
			return true, 0
		}
		t.st.Scalars++
	}
	return false, adv
}

// bomReject 在流首 BOM 被证伪时调用：hold 中的旧前缀按正常字节重放，
// 本次 p 全部保留。逐字节内联解析直到需要等待或出错。
func (t *Transcoder) bomReject(p []byte) (bool, int) {
	t.bom = bomNone
	buf := append(append([]byte{}, t.hold...), p...)
	fromHold := len(t.hold)
	t.hold = t.hold[:0]
	consumed := 0
	for consumed < len(buf) {
		stop, adv := t.feed(buf[consumed:])
		consumed += adv
		if stop || adv == 0 {
			break
		}
	}
	if consumed <= fromHold {
		// 全部停在旧前缀内：本次 p 尚未消耗（字节进 hold 等待）
		return true, 0
	}
	return t.term != nil, consumed - fromHold
}

func (t *Transcoder) encLen(r rune) int {
	if t.cfg.Dir <= U8toU16BE {
		return u16.EncLen(r)
	}
	return u8.EncLen(r)
}

func (t *Transcoder) appendEnc(dst []byte, r rune) []byte {
	if t.cfg.Dir == U8toU16LE {
		return u16.EncodeAppend(dst, u16.LE, r)
	}
	if t.cfg.Dir == U8toU16BE {
		return u16.EncodeAppend(dst, u16.BE, r)
	}
	return u8.EncodeAppend(dst, r)
}
