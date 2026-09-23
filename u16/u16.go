// Package u16 在字节级实现 UTF-16LE/UTF-16BE 解码与编码。
package u16

import "ontology/scalar"

// Kind 与 u8.Kind 同义：合法 / 非法单元 / 截断。
type Kind int

const (
	OK    Kind = iota
	Bad
	Short
)

// Unit 是一次 UTF-16 解码结果，N 为消费的字节数（2 或 4）。
type Unit struct {
	Kind Kind
	R    rune
	N    int
	// Looked 为解码实际检视到的字节数。
	Looked int
}

func unit16(p []byte, at int, little bool) uint16 {
	if little {
		return uint16(p[at]) | uint16(p[at+1])<<8
	}
	return uint16(p[at])<<8 | uint16(p[at+1])
}

// DecodeAt 从 p[at:] 解码一个 UTF-16 单元。
// little 选择 LE/BE；available 为从 at 起可读字节数。
func DecodeAt(p []byte, at, available int, little bool) Unit {
	if available < 2 {
		return Unit{Kind: Short, R: scalar.Replacement, N: available, Looked: available}
	}
	u := unit16(p, at, little)
	switch {
	case scalar.HighSurrogate(u):
		if available < 4 {
			return Unit{Kind: Short, R: scalar.Replacement, N: 2, Looked: available}
		}
		lo := unit16(p, at+2, little)
		if scalar.LowSurrogate(lo) {
			r := 0x10000 + (rune(u)-0xD800)<<10 + (rune(lo) - 0xDC00)
			return Unit{Kind: OK, R: r, N: 4, Looked: 4}
		}
		return Unit{Kind: Bad, R: scalar.Replacement, N: 2, Looked: 4}
	case scalar.LowSurrogate(u):
		return Unit{Kind: Bad, R: scalar.Replacement, N: 2, Looked: 2}
	default:
		return Unit{Kind: OK, R: rune(u), N: 2, Looked: 2}
	}
}

// Encode 把标量追加编码为 UTF-16（little 选 LE/BE），非法标量不追加。
func Encode(dst []byte, r rune, little bool) []byte {
	put := func(u uint16) []byte {
		if little {
			return append(dst, byte(u), byte(u>>8))
		}
		return append(dst, byte(u>>8), byte(u))
	}
	if !scalar.Valid(r) {
		return dst
	}
	if r < 0x10000 {
		return put(uint16(r))
	}
	r -= 0x10000
	hi := uint16(0xD800 + (r>>10)&0x3FF)
	lo := uint16(0xDC00 + r&0x3FF)
	dst = put(hi)
	return put(lo)
}
