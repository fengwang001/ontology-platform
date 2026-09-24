// Package u16 在字节级别实现 UTF-16LE / UTF-16BE 的增量解码与编码。
// 不使用 unicode/utf16。
package u16

import "ontology/scalar"

// Order 是 UTF-16 字节序。
type Order uint8

const (
	LE Order = iota
	BE
)

// Kind 是一次解码事件的类型。
type Kind uint8

const (
	Scalar Kind = iota
	Bad         // 一个非法 code unit（孤立代理）
)

// Event 是一次解码事件。Bytes 为该单元占用的字节数（2）。
type Event struct {
	R     rune
	Kind  Kind
	Bytes int
}

// Decoder 是按 code unit 推进的增量解码器；零值不可直接用，见 New。
type Decoder struct {
	ord     Order
	hi      uint16 // 已见高代理；0 表示无
	pend    byte
	hasPend bool
}

// New 返回指定字节序的解码器。
func New(ord Order) *Decoder { return &Decoder{ord: ord} }

// Reset 清空半个 code unit 与待配对高代理。
func (d *Decoder) Reset() { d.hi, d.pend, d.hasPend = 0, 0, false }

// HalfByte 报告是否残留半个 code unit。
func (d *Decoder) HalfByte() bool { return d.hasPend }

// LoneHigh 报告是否残留一个等待低代理的高代理。
func (d *Decoder) LoneHigh() bool { return d.hi != 0 }

func (d *Decoder) unit(a, b byte) uint16 {
	if d.ord == LE {
		return uint16(a) | uint16(b)<<8
	}
	return uint16(a)<<8 | uint16(b)
}

// StepUnit 处理一个完整 code unit（字节对）。
// 高代理后非低代理时返回两个事件：高代理为 Bad，非低代理 unit 重新解析。
func (d *Decoder) StepUnit(u uint16) []Event {
	if d.hi != 0 {
		hi := d.hi
		d.hi = 0
		if scalar.LowSurrogate(u) {
			return []Event{{R: scalar.CombineSurrogates(hi, u), Kind: Scalar, Bytes: 4}}
		}
		return append([]Event{{Kind: Bad, Bytes: 2}}, d.StepUnit(u)...)
	}
	switch {
	case scalar.HighSurrogate(u):
		d.hi = u
		return nil
	case scalar.LowSurrogate(u):
		return []Event{{Kind: Bad, Bytes: 2}}
	default:
		return []Event{{R: rune(u), Kind: Scalar, Bytes: 2}}
	}
}

// Step 喂入一个字节，返回闭合事件（可能多个）。
func (d *Decoder) Step(b byte) []Event {
	if !d.hasPend {
		d.pend, d.hasPend = b, true
		return nil
	}
	var u uint16
	if d.ord == LE {
		u = uint16(d.pend) | uint16(b)<<8
	} else {
		u = uint16(d.pend)<<8 | uint16(b)
	}
	d.pend, d.hasPend = 0, false
	return d.StepUnit(u)
}

// Flush 在流结束时返回截断（半个 unit 或孤立高代理）：调用方据此区分。
func (d *Decoder) Flush() (halfByte, loneHigh bool) {
	return d.hasPend, d.hi != 0
}

// Encode 把标量 r 按字节序编码成 UTF-16 字节（增补平面为代理对）。
func Encode(r rune, ord Order) []byte {
	var units []uint16
	if r >= 0x10000 {
		r -= 0x10000
		hi := uint16(0xD800 + r>>10)
		lo := uint16(0xDC00 + r&0x3FF)
		units = []uint16{hi, lo}
	} else {
		units = []uint16{uint16(r)}
	}
	out := make([]byte, 0, len(units)*2)
	for _, u := range units {
		if ord == LE {
			out = append(out, byte(u), byte(u>>8))
		} else {
			out = append(out, byte(u>>8), byte(u))
		}
	}
	return out
}
