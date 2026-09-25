// Package u16 在字节级实现 UTF-16LE/UTF-16BE 的逐标量解码与编码。
package u16

import "ontology/scalar"

// Order 是 UTF-16 字节序。
type Order int

const (
	LE Order = iota
	BE
)

// Unit 描述一次解码结果。OK 为真时 R 是标量、Len 是占用字节数（2 或 4）；
// OK 为假表示一个非法单元（孤立代理），Len=2，调用方须从下一码元重解析；
// Len==0 表示数据不足（奇数残留或高代理后流结束）。
type Unit struct {
	R   rune
	Len int
	OK  bool
}

func codeUnit(p []byte, o Order) uint16 {
	if o == LE {
		return uint16(p[0]) | uint16(p[1])<<8
	}
	return uint16(p[0])<<8 | uint16(p[1])
}

// First 从 p 起点解码一个标量，语义见 Unit。
func First(p []byte, o Order) Unit {
	if len(p) < 2 {
		return Unit{}
	}
	u := codeUnit(p[:2], o)
	switch {
	case scalar.IsHighSurrogate(u):
		if len(p) < 4 {
			return Unit{} // 高代理后数据不足
		}
		lo := codeUnit(p[2:4], o)
		if scalar.IsLowSurrogate(lo) {
			return Unit{R: scalar.FromSurrogatePair(u, lo), Len: 4, OK: true}
		}
		return Unit{Len: 2} // 高代理后跟非低代理：仅吞高代理，后者重解析
	case scalar.IsLowSurrogate(u):
		return Unit{Len: 2} // 孤立低代理
	default:
		return Unit{R: rune(u), Len: 2, OK: true}
	}
}

func putUnit(dst []byte, u uint16, o Order) []byte {
	if o == LE {
		return append(dst, byte(u), byte(u>>8))
	}
	return append(dst, byte(u>>8), byte(u))
}

// Encode 把标量 r 按字节序 o 追加到 dst。
func Encode(dst []byte, r rune, o Order) []byte {
	if r < 0x10000 {
		return putUnit(dst, uint16(r), o)
	}
	hi, lo := scalar.ToSurrogatePair(r)
	dst = putUnit(dst, hi, o)
	return putUnit(dst, lo, o)
}

// EncLen 返回 r 编码后的字节数。
func EncLen(r rune) int {
	if r < 0x10000 {
		return 2
	}
	return 4
}

// DetectOrder 由流首两个字节判定字节序：FEFF→LE 视角下的真实序；
// 返回识别出的序、以及该 BOM 是否有效（FFFE 为非法字节序标记）。
func DetectOrder(p []byte) (Order, bool) {
	if len(p) < 2 {
		return LE, false
	}
	switch codeUnit(p[:2], LE) {
	case 0xFEFF:
		return LE, true
	case 0xFFFE:
		return BE, true
	}
	return LE, false
}

// BOMBytes 返回指定字节序的 BOM 字节。
func BOMBytes(o Order) []byte {
	if o == LE {
		return []byte{0xFF, 0xFE}
	}
	return []byte{0xFE, 0xFF}
}
