// Package u16 提供 UTF-16LE / UTF-16BE 的单元级解码与编码，
// 含代理对组合与拆分；孤立代理的判定由调用方（stream）完成。
package u16

import "ontology/scalar"

// Unit 把 2 个字节按字节序解释为一个 UTF-16 代码单元。
func Unit(b []byte, be bool) uint16 {
	if be {
		return uint16(b[0])<<8 | uint16(b[1])
	}
	return uint16(b[1])<<8 | uint16(b[0])
}

// IsHigh 报告 u 是否为高代理。
func IsHigh(u uint16) bool { return u >= 0xD800 && u <= 0xDBFF }

// IsLow 报告 u 是否为低代理。
func IsLow(u uint16) bool { return u >= 0xDC00 && u <= 0xDFFF }

// Combine 把高、低代理组合为标量值（>= 0x10000）。
func Combine(hi, lo uint16) int32 {
	return 0x10000 + (int32(hi)-0xD800)<<10 + (int32(lo) - 0xDC00)
}

// AppendUnit 按字节序追加一个 16 位单元。
func AppendUnit(dst []byte, u uint16, be bool) []byte {
	if be {
		return append(dst, byte(u>>8), byte(u))
	}
	return append(dst, byte(u), byte(u>>8))
}

// AppendEncode 把合法标量 r 以 UTF-16（指定字节序）追加到 dst；
// r >= 0x10000 时编成代理对（4 字节，不可分割）。
func AppendEncode(dst []byte, r int32, be bool) []byte {
	if r < 0x10000 {
		return AppendUnit(dst, uint16(r), be)
	}
	v := r - 0x10000
	dst = AppendUnit(dst, uint16(0xD800+v>>10), be)
	return AppendUnit(dst, uint16(0xDC00+v&0x3FF), be)
}

// AppendReplacement 追加 U+FFFD 的 UTF-16 编码。
func AppendReplacement(dst []byte, be bool) []byte {
	return AppendUnit(dst, scalar.Replacement, be)
}

// Decoder 是 UTF-16（指定字节序）的增量逐字节解码器，回调约定同 u8.Decoder。
type Decoder struct {
	be   bool
	pend []byte
}

// NewDecoder 返回空的解码器；be 表示大端。
func NewDecoder(be bool) *Decoder { return &Decoder{be: be} }

// Pending 返回缓存中未决的字节数（硬上限 3，见 DESIGN.md 第 2 节）。
func (d *Decoder) Pending() int { return len(d.pend) }

// Feed 喂入一个字节；每完成一个单元触发一次回调。
func (d *Decoder) Feed(b byte, emit func(r int32, n int, ok bool)) {
	d.pend = append(d.pend, b)
	for {
		switch len(d.pend) {
		case 1, 3:
			return
		case 2:
			u := Unit(d.pend, d.be)
			if IsHigh(u) {
				return
			}
			if IsLow(u) {
				emit(0, 2, false)
			} else {
				emit(int32(u), 2, true)
			}
			d.pend = d.pend[:0]
			return
		}
		if u2 := Unit(d.pend[2:], d.be); IsLow(u2) {
			emit(Combine(Unit(d.pend, d.be), u2), 4, true)
			d.pend = d.pend[:0]
			return
		}
		emit(0, 2, false) // 孤立高代理；其后的单元被重新当作新字符
		rest := append([]byte(nil), d.pend[2:]...)
		d.pend = append(d.pend[:0], rest...)
	}
}

// Flush 把残留的高代理和/或奇数残余字节各作为一个非法单元上报并清空。
func (d *Decoder) Flush(emit func(r int32, n int, ok bool)) {
	if len(d.pend) >= 2 {
		emit(0, 2, false)
		d.pend = d.pend[2:]
	}
	if len(d.pend) == 1 {
		emit(0, 1, false)
		d.pend = d.pend[:0]
	}
}
