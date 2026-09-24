// Package u16 解码/编码 UTF-16LE 与 UTF-16BE，处理代理对与孤立代理，判定全手写。
package u16

import "ontology/scalar"

// Order 表示 UTF-16 字节序。
type Order int

const (
	LE Order = iota
	BE
)

// Unit 是一次 UTF-16 解码结果。
// Size：消费字节数；0=奇数字节残留需等更多数据。
// Bad：非法单元；此时 Size 为该非法单元吞掉的字节数（孤立代理=2）。
// 高代理后的非低代理不会被吞掉：此时返回 Size==2、Bad==true、Keep==true，
// 调用方从那个非低代理码元重新开始。
type Unit struct {
	Rune rune
	Size int
	Bad  bool
	Keep bool
}

// DecodeAt 按 ord 解码 b 开头的一个标量/非法单元。
func DecodeAt(ord Order, b []byte) Unit {
	if len(b) < 2 {
		return Unit{}
	}
	u0 := cu(ord, b[0], b[1])
	switch {
	case scalar.IsHighSurrogate(rune(u0)):
		if len(b) < 4 {
			return Unit{} // 高代理，等待配对
		}
		u1 := cu(ord, b[2], b[3])
		if scalar.IsLowSurrogate(rune(u1)) {
			r, _ := scalar.SurrogatePair(rune(u0), rune(u1))
			return Unit{Rune: r, Size: 4}
		}
		return Unit{Rune: scalar.Replacement, Size: 2, Bad: true, Keep: true}
	case scalar.IsLowSurrogate(rune(u0)):
		return Unit{Rune: scalar.Replacement, Size: 2, Bad: true}
	}
	return Unit{Rune: rune(u0), Size: 2}
}

func cu(ord Order, a, b byte) uint16 {
	if ord == LE {
		return uint16(a) | uint16(b)<<8
	}
	return uint16(a)<<8 | uint16(b)
}

// EncLen 返回标量 r 编码为 UTF-16 的字节数（2 或 4）。
func EncLen(r rune) int {
	if r >= 0x10000 {
		return 4
	}
	return 2
}

// EncodeAppend 按 ord 把标量 r 追加到 dst。
func EncodeAppend(dst []byte, ord Order, r rune) []byte {
	put := func(u uint16) {
		if ord == LE {
			dst = append(dst, byte(u), byte(u>>8))
		} else {
			dst = append(dst, byte(u>>8), byte(u))
		}
	}
	if hi, lo, ok := scalar.SplitSurrogate(r); ok {
		put(uint16(hi))
		put(uint16(lo))
	} else {
		put(uint16(r))
	}
	return dst
}

// Encode 返回标量 r 的 UTF-16 编码。
func Encode(ord Order, r rune) []byte { return EncodeAppend(nil, ord, r) }

// BOM 字节序列。
var BOMLE = []byte{0xFF, 0xFE}
var BOMBE = []byte{0xFE, 0xFF}
