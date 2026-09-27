package u8

import "ontology/scalar"

// Unit 是一次解码结果：合法标量或一个非法单元；Len 是该单元吞掉的字节数。
type Unit struct {
	R   scalar.Rune
	Len int
}

// Decoder 是逐字节增量 UTF-8 解码器；缓存的是已解码出的部分码点，不回扫原字节。
type Decoder struct {
	lead    byte
	have    bool
	need    int
	got     int
	acc     rune
	Checked int64
}

func NewDecoder() *Decoder { return &Decoder{} }

// Step 喂入一个字节。返回的 Unit.Len==0 表示尚无单元完成；consumed=false 时
// b 未被消费（前一首字节刚刚单独构成非法单元），调用方应用同一个 b 再调一次。
func (d *Decoder) Step(b byte) (u Unit, consumed bool) {
	d.Checked++
	if !d.have {
		return d.start(b)
	}
	ok := false
	if d.got == 0 {
		ok = secondOK(d.lead, b)
	} else {
		ok = b >= 0x80 && b <= 0xBF
	}
	if !ok {
		d.have = false
		return Unit{R: scalar.Rune{Valid: false}, Len: 1 + d.got}, false
	}
	d.acc = d.acc<<6 | rune(b&0x3F)
	d.got++
	if d.got+1 < d.need {
		return Unit{}, true
	}
	d.have = false
	return Unit{R: scalar.Rune{Value: d.acc, Valid: scalar.Scalar(d.acc)}, Len: d.need}, true
}

func (d *Decoder) start(b byte) (Unit, bool) {
	switch {
	case b < 0x80:
		return Unit{R: scalar.Rune{Value: rune(b), Valid: true}, Len: 1}, true
	case b < 0xC2:
		return Unit{R: scalar.Rune{Valid: false}, Len: 1}, true
	case b < 0xE0:
		d.set(b, 2, rune(b&0x1F))
	case b == 0xE0, b >= 0xE1 && b <= 0xEC, b == 0xED, b >= 0xEE && b <= 0xEF:
		d.set(b, 3, rune(b&0x0F))
	case b == 0xF0, b >= 0xF1 && b <= 0xF3, b == 0xF4:
		d.set(b, 4, rune(b&0x07))
	default:
		return Unit{R: scalar.Rune{Valid: false}, Len: 1}, true
	}
	return Unit{}, true
}

func (d *Decoder) set(lead byte, need int, acc rune) {
	d.lead, d.have, d.need, d.got, d.acc = lead, true, need, 0, acc
}

func secondOK(lead, b byte) bool {
	if b < 0x80 || b > 0xBF {
		return false
	}
	switch lead {
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

// EOF 标记流结束：残留前缀作为一个非法（截断）单元吐出。
func (d *Decoder) EOF() Unit {
	if !d.have {
		return Unit{}
	}
	d.have = false
	return Unit{R: scalar.Rune{Valid: false}, Len: 1 + d.got}
}

// Pending 返回当前缓存占用的输入字节数（硬上限 3）。
func (d *Decoder) Pending() int {
	if d.have {
		return 1 + d.got
	}
	return 0
}

// EncodeLen 返回标量的 UTF-8 编码字节数，非法返回 0。
func EncodeLen(r rune) int {
	switch {
	case !scalar.Scalar(r):
		return 0
	case r < 0x80:
		return 1
	case r < 0x800:
		return 2
	case r < 0x10000:
		return 3
	default:
		return 4
	}
}

// Encode 把标量编码成 UTF-8。
func Encode(r rune) []byte {
	switch n := EncodeLen(r); n {
	case 1:
		return []byte{byte(r)}
	case 2:
		return []byte{0xC0 | byte(r>>6), 0x80 | byte(r&0x3F)}
	case 3:
		return []byte{0xE0 | byte(r>>12), 0x80 | byte(r>>6&0x3F), 0x80 | byte(r&0x3F)}
	case 4:
		return []byte{0xF0 | byte(r>>18), 0x80 | byte(r>>12&0x3F),
			0x80 | byte(r>>6&0x3F), 0x80 | byte(r&0x3F)}
	default:
		return nil
	}
}

// AlignStart 返回切分点 cut 处解析必须回退到的起点（回看 ≤3 字节）。
func AlignStart(in []byte, cut int) int {
	h := cut
	for h > 0 && in[h-1] >= 0x80 && in[h-1] <= 0xBF && cut-h < 3 {
		h--
	}
	if h > 0 && h-1 < cut {
		if lead := in[h-1]; lead >= 0xC2 && lead <= 0xF4 && h < len(in) {
			if n := declaredLen(lead); n >= 2 && secondOK(lead, in[h]) {
				h--
			}
		}
	}
	return h
}

func declaredLen(b byte) int {
	switch {
	case b >= 0xC2 && b <= 0xDF:
		return 2
	case b >= 0xE0 && b <= 0xEF:
		return 3
	default:
		return 4
	}
}
