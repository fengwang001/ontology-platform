// Package u16 在字节级别完成 UTF-16LE/BE 的组合与编码，依赖 scalar。
package u16

import "ontology/scalar"

const (
	LE = iota
	BE
)

// Unit 按字节序把两个字节拼成 16 位码元（可能是代理）。
func Unit(a, b byte, order int) rune {
	if order == BE {
		return rune(a)<<8 | rune(b)
	}
	return rune(b)<<8 | rune(a)
}

// IsHigh/IsLow 判定高/低代理。
func IsHigh(u rune) bool { return 0xD800 <= u && u <= 0xDBFF }
func IsLow(u rune) bool  { return 0xDC00 <= u && u <= 0xDFFF }

// Pair 组合合法代理对为标量。
func Pair(hi, lo rune) rune {
	return 0x10000 + (hi-0xD800)<<10 + (lo - 0xDC00)
}

// Encode 把标量写入 p（UTF-16，容量至少 4），返回字节数。
func Encode(r rune, order int, p []byte) int {
	if !scalar.Valid(r) {
		return 0
	}
	put := func(i int, u uint16) {
		a, b := byte(u>>8), byte(u)
		if order == LE {
			a, b = b, a
		}
		p[i], p[i+1] = a, b
	}
	if r < 0x10000 {
		put(0, uint16(r))
		return 2
	}
	r -= 0x10000
	put(0, 0xD800+uint16(r>>10))
	put(2, 0xDC00+uint16(r&0x3FF))
	return 4
}
