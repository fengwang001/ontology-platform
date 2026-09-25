// Package u8 逐字节实现 UTF-8 解码状态机与编码，不使用 unicode/utf8。
package u8

import "ontology/scalar"

// MaxSeqLen 是 UTF-8 序列最大字节数。
const MaxSeqLen = 4

// Unit 是一次解码裁决：一个合法标量或一个非法/截断单元。
type Unit struct {
	R     rune
	Size  int  // 本单元吞掉的字节数
	Bad   bool // 非法单元
	Trunc bool // Close 时残留的未完成前缀
}

// Decoder 是跨 Write 复用的 UTF-8 状态机。
type Decoder struct {
	need int  // 还差几个续字节；0 表示等待首字节
	low  byte // 下一续字节下界
	cp   rune // 已累积码位
	head byte // 首字节
	got  int  // 已读字节数（含首字节）
}

// Pending 返回缓存中的字节数（不含首字节已裁决的情形）。
func (d *Decoder) Pending() int {
	if d.need == 0 {
		return 0
	}
	return d.got
}

// Feed 喂入一个字节。ready 为 false 表示字节已被内部回退机制消耗但暂不产单元；
// 当产出 Unit 时 Size≥1。检查计数由调用方在外层统计（进入本方法即检查一次）。
func (d *Decoder) Feed(b byte) (Unit, bool) {
	if d.need > 0 {
		if b < d.low || b > 0xBF {
			u := Unit{Bad: true, Size: d.got}
			d.reset()
			return u, false // b 作为新首字节由上层重喂
		}
		d.cp = d.cp<<6 | rune(b&0x3F)
		d.low = 0x80
		d.need--
		d.got++
		if d.need == 0 {
			return Unit{R: d.cp, Size: d.got}, true
		}
		return Unit{}, true
	}
	d.head = b
	d.got = 1
	switch {
	case b < 0x80:
		return Unit{R: rune(b), Size: 1}, true
	case b >= 0xC2 && b <= 0xDF:
		d.need, d.low, d.cp = 1, 0x80, rune(b&0x1F)
	case b == 0xE0:
		d.need, d.low, d.cp = 2, 0xA0, rune(b&0x0F)
	case b >= 0xE1 && b <= 0xEF:
		d.need, d.low, d.cp = 2, 0x80, rune(b&0x0F)
		if b == 0xED {
			d.low = 0x9F
		}
	case b == 0xF0:
		d.need, d.low, d.cp = 3, 0x90, rune(b&0x07)
	case b >= 0xF1 && b <= 0xF3:
		d.need, d.low, d.cp = 3, 0x80, rune(b&0x07)
	case b == 0xF4:
		d.need, d.low, d.cp = 3, 0x8F, rune(b&0x07)
	default: // 80..BF、C0、C1、F5..FF
		return Unit{Bad: true, Size: 1}, true
	}
	return Unit{}, true
}

// Close 裁决流结束时的残留：替换模式下它是一个截断单元。
func (d *Decoder) Close() (Unit, bool) {
	if d.need == 0 {
		return Unit{}, true
	}
	u := Unit{Bad: true, Trunc: true, Size: d.got}
	d.reset()
	return u, true
}

func (d *Decoder) reset() { d.need, d.got, d.cp, d.head = 0, 0, 0, 0 }

// Encode 把标量编码成 UTF-8 字节；非法标量返回 nil。
func Encode(r rune) []byte {
	if !scalar.IsScalar(r) {
		return nil
	}
	switch {
	case r < 0x80:
		return []byte{byte(r)}
	case r < 0x800:
		return []byte{0xC0 | byte(r>>6), 0x80 | byte(r&0x3F)}
	case r < 0x10000:
		return []byte{
			0xE0 | byte(r>>12),
			0x80 | byte((r>>6)&0x3F),
			0x80 | byte(r&0x3F),
		}
	default:
		return []byte{
			0xF0 | byte(r>>18),
			0x80 | byte((r>>12)&0x3F),
			0x80 | byte((r>>6)&0x3F),
			0x80 | byte(r&0x3F),
		}
	}
}
