// Package u8 提供手写字节级的 UTF-8 逐标量解码与编码。
package u8

import "ontology/scalar"

// BOM 是 UTF-8 形式的 U+FEFF。
var BOM = []byte{0xEF, 0xBB, 0xBF}

const maxPending = 3 // 一个未闭合合法前缀除首字节外最多 3 字节

// Unit 是一次解码结果：合法标量或一个非法单元。
type Unit struct {
	R     rune  // 合法时为标量；非法时为 scalar.Replacement
	Valid bool  // 是否合法标量
	Size  int   // 本单元吞掉的输入字节数
	Start int64 // 本单元首字节在整个输入流中的偏移
	Trunc bool  // 仅非法且原因为流结束截断时为真
}

// Decoder 是跨 Write 切分点的 UTF-8 流式解码器。
// 单实例非并发安全。
type Decoder struct {
	need     uint8 // 期望总长；0 表示空闲
	got      uint8 // 已收字节（含首字节）
	b0       byte
	acc      rune
	startOff int64
	Checked  int64
}

// PendingLen 返回已缓存但尚未闭合的合法前缀字节数（≤3）。
func (d *Decoder) PendingLen() int { return int(d.got) }

// Feed 喂入一块"新"字节（base 为首字节全局偏移），返回本次闭合的单元。
// 调用方负责把上次未消费的挂起前缀从重发缓冲中跳过，保证每个字节只喂一次。
func (d *Decoder) Feed(in []byte, base int64) []Unit {
	var us []Unit
	for i, c := range in {
		at := base + int64(i)
		d.Checked++
		if d.need == 0 {
			d.startByte(c, at)
			if d.need <= 1 { // ASCII 或单字节非法
				us = d.emitIdle(us, c, at)
			}
			continue
		}
		if c&0xC0 == 0x80 && (d.got > 1 || secondOK(d.b0, c)) {
			d.acc = d.acc<<6 | rune(c&0x3F)
			d.got++
			if d.got == d.need {
				us = append(us, Unit{R: d.acc, Valid: true, Size: int(d.need), Start: d.startOff})
				d.need = 0
			}
			continue
		}
		us = append(us, Unit{R: scalar.Replacement, Size: int(d.got) + 1, Start: d.startOff})
		d.need = 0
		d.startByte(c, at)
		if d.need <= 1 {
			us = d.emitIdle(us, c, at)
		}
	}
	return us
}

// Flush 在流结束时调用：仍有合法前缀则产出一个截断非法单元。
func (d *Decoder) Flush() (Unit, bool) {
	if d.need == 0 {
		return Unit{}, false
	}
	u := Unit{R: scalar.Replacement, Size: int(d.got), Start: d.startOff, Trunc: true}
	d.need = 0
	return u, true
}

func (d *Decoder) startByte(b byte, at int64) {
	d.startOff = at
	switch {
	case b < 0x80:
		d.need, d.acc = 1, rune(b)
	case b < 0xC2, b > 0xF4:
		d.need, d.acc = 1, scalar.Replacement
	default:
		d.need = firstLen(b)
		d.got, d.b0, d.acc = 1, b, rune(b&(0xFF>>(d.need+1)))
	}
}

func (d *Decoder) emitIdle(us []Unit, b byte, at int64) []Unit {
	if b < 0x80 {
		us = append(us, Unit{R: rune(b), Valid: true, Size: 1, Start: at})
	} else {
		us = append(us, Unit{R: scalar.Replacement, Size: 1, Start: at})
	}
	return us
}

func firstLen(b byte) uint8 {
	switch {
	case b < 0xE0:
		return 2
	case b < 0xF0:
		return 3
	default:
		return 4
	}
}

// secondOK 报告 c 作为首字节 b 的第二字节是否落在合法区间（c 已是续字节）。
func secondOK(b, c byte) bool {
	switch b {
	case 0xE0:
		return c >= 0xA0
	case 0xED:
		return c <= 0x9F
	case 0xF0:
		return c >= 0x90
	case 0xF4:
		return c <= 0x8F
	default:
		return true
	}
}

// Encode 把合法标量编码为 UTF-8，追加到 dst。
func Encode(dst []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(dst, byte(r))
	case r < 0x800:
		return append(dst, 0xC0|byte(r>>6), 0x80|byte(r&0x3F))
	case r < 0x10000:
		return append(dst, 0xE0|byte(r>>12), 0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	default:
		return append(dst, 0xF0|byte(r>>18), 0x80|byte(r>>12)&0x3F,
			0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	}
}
