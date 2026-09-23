// Package u8 逐字节解码/编码 UTF-8，不使用 unicode/utf8。
package u8

import "ontology/scalar"

// Unit 是一次解码结果：合法标量或一个非法单元。
type Unit struct {
	R       rune
	Valid   bool
	Start   int // 单元首字节在整个输入流中的偏移
	Len     int // 本单元吞掉的字节数
	Refeed  bool // 触发字节未被吞，需作为新首字节重新喂入
	RefeedB byte
}

func cont(b byte) bool { return b&0xC0 == 0x80 }

// Decoder 是逐喂入的 UTF-8 状态机。每个输入字节至多检查两次（回炉一次）。
type Decoder struct {
	off     int  // 下一个喂入字节在流中的偏移
	pend    int  // 已收下的前缀字节数（0 表示空闲）
	lead    byte // 首字节
	need    int  // 还需续字节数
	val     rune // 累积码点
	refeed  bool // 有待重新喂入的字节
	refeedB byte
}

// Pending 返回切分缓存中的字节数（硬上限 3）。
func (d *Decoder) Pending() int { return d.pend }

// Retry 取出需回炉的字节；返回 ok=false 表示应喂入新字节。
func (d *Decoder) Retry() (b byte, off int, ok bool) {
	if d.refeed {
		d.refeed = false
		return d.refeedB, d.off - 1, true
	}
	return 0, 0, false
}

// Feed 喂入一个字节，返回一个已完成单元与是否产出。
func (d *Decoder) Feed(b byte) (Unit, bool) {
	d.off++
	if d.pend == 0 {
		u := d.leadStart(b)
		return u, u.Len > 0
	}
	return d.contByte(b)
}

func (d *Decoder) leadStart(b byte) Unit {
	switch {
	case b < 0x80:
		return Unit{R: rune(b), Valid: true, Start: d.off - 1, Len: 1}
	case cont(b):
		return Unit{Valid: false, Start: d.off - 1, Len: 1}
	case b >= 0xC2 && b <= 0xDF:
		d.lead, d.pend, d.need, d.val = b, 1, 1, rune(b&0x1F)
	case b == 0xE0:
		d.lead, d.pend, d.need, d.val = b, 1, 2, 0
	case b >= 0xE1 && b <= 0xEF:
		d.lead, d.pend, d.need, d.val = b, 1, 2, rune(b&0x0F)
	case b == 0xF0:
		d.lead, d.pend, d.need, d.val = b, 1, 3, 0
	case b >= 0xF1 && b <= 0xF3:
		d.lead, d.pend, d.need, d.val = b, 1, 3, rune(b&0x07)
	case b == 0xF4:
		d.lead, d.pend, d.need, d.val = b, 1, 3, 0
	default: // C0 C1 F5..FF
		return Unit{Valid: false, Start: d.off - 1, Len: 1}
	}
	return Unit{}
}

func (d *Decoder) contByte(b byte) (Unit, bool) {
	start := d.off - 1 - d.pend
	ok := cont(b)
	if ok && d.need == d.expectedCont()-1 {
		ok = d.secondOK(b)
	}
	if !ok {
		n := d.pend
		d.pend, d.need = 0, 0
		d.refeed, d.refeedB = true, b
		return Unit{Valid: false, Start: start, Len: n, Refeed: true, RefeedB: b}, true
	}
	d.val = d.val<<6 | rune(b&0x3F)
	d.pend++
	d.need--
	if d.need == 0 {
		r := d.val
		d.pend = 0
		return Unit{R: r, Valid: scalar.IsScalar(r), Start: start, Len: d.pendLen()}, true
	}
	return Unit{}, false
}

func (d *Decoder) expectedCont() int {
	if d.lead >= 0xF0 {
		return 3
	}
	return 2
}

func (d *Decoder) pendLen() int {
	if d.lead >= 0xF0 {
		return 4
	}
	if d.lead >= 0xE0 {
		return 3
	}
	return 2
}

func (d *Decoder) secondOK(b byte) bool {
	switch d.lead {
	case 0xE0:
		return b >= 0xA0
	case 0xED:
		return b <= 0x9F
	case 0xF0:
		return b >= 0x90
	case 0xF4:
		return b <= 0x8F
	}
	return true
}

// Flush 在流结束时调用：残留合法前缀作为一个非法单元。
func (d *Decoder) Flush() (Unit, bool) {
	if d.pend == 0 {
		return Unit{}, false
	}
	u := Unit{Valid: false, Start: d.off - d.pend, Len: d.pend}
	d.pend, d.need = 0, 0
	return u, true
}

// Encode 把合法标量编码为 UTF-8。
func Encode(r rune) []byte {
	switch {
	case r < 0x80:
		return []byte{byte(r)}
	case r < 0x800:
		return []byte{0xC0 | byte(r>>6), 0x80 | byte(r&0x3F)}
	case r < 0x10000:
		return []byte{0xE0 | byte(r>>12), 0x80 | byte(r>>6&0x3F), 0x80 | byte(r&0x3F)}
	default:
		return []byte{0xF0 | byte(r>>18), 0x80 | byte(r>>12&0x3F), 0x80 | byte(r>>6&0x3F), 0x80 | byte(r&0x3F)}
	}
}
