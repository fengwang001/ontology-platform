// Package u16 手写 UTF-16LE/UTF-16BE 的解码与编码。
package u16

import "ontology/scalar"

// Endian 标记字节序。
type Endian int

const (
	LE Endian = iota
	BE
)

// Unit 返回 b[0:2] 按序解释出的 16 位单元。
func Unit(b []byte, e Endian) uint16 {
	if e == LE {
		return uint16(b[0]) | uint16(b[1])<<8
	}
	return uint16(b[0])<<8 | uint16(b[1])
}

// PutUnit 把单元写入 b[0:2]。
func PutUnit(b []byte, u uint16, e Endian) {
	if e == LE {
		b[0], b[1] = byte(u), byte(u>>8)
	} else {
		b[0], b[1] = byte(u>>8), byte(u)
	}
}

// Decode 从 b（绝对起点为偶偏移）解码一个 UTF-16 单元。
// 返回 r、消费字节数 size；more=true 表示字节未到齐；valid=false 表示孤立代理。
func Decode(b []byte, e Endian) (r rune, size int, valid, more bool) {
	if len(b) < 2 {
		return 0, len(b), false, true
	}
	u := Unit(b, e)
	if scalar.HighSurrogate(rune(u)) {
		if len(b) < 4 {
			return 0, len(b), false, true
		}
		lo := Unit(b[2:], e)
		if scalar.LowSurrogate(rune(lo)) {
			return scalar.SurrogatePair(u, lo), 4, true, false
		}
		return 0, 2, false, false
	}
	if scalar.IsSurrogate(rune(u)) {
		return 0, 2, false, false
	}
	return rune(u), 2, true, false
}

// Encode 把标量编码成 UTF-16 字节；非 Scalar 编成孤立单元 FFFD。
func Encode(r rune, e Endian) []byte {
	if !scalar.IsScalar(r) {
		r = 0xFFFD
	}
	if r < 0x10000 {
		out := make([]byte, 2)
		PutUnit(out, uint16(r), e)
		return out
	}
	hi, lo := scalar.EncodeSurrogates(r)
	out := make([]byte, 4)
	PutUnit(out, hi, e)
	PutUnit(out[2:], lo, e)
	return out
}

// BOM 返回指定字节序的流首 BOM 字节。
func BOM(e Endian) []byte {
	if e == LE {
		return []byte{0xFF, 0xFE}
	}
	return []byte{0xFE, 0xFF}
}

// SplitStart 返回内部切点 c 对齐到 UTF-16 单元/代理对边界后的起点。
func SplitStart(b []byte, c int, e Endian) int {
	if c <= 0 {
		return 0
	}
	if c%2 == 1 {
		c--
	}
	if c == 0 {
		return 0
	}
	if scalar.HighSurrogate(rune(Unit(b[c-2:], e))) {
		return c - 2
	}
	return c
}
