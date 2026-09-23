// Package u8 在字节级实现 UTF-8 逐标量解码与编码。
// 禁用 unicode/utf8 与任何隐式解码；字节判定全部手写。
package u8

import "ontology/scalar"

// Kind 标识一次解码结果。
type Kind int

const (
	OK      Kind = iota // 合法标量
	Bad                 // 确定的非法单元（非法字节）
	Short               // 合法前缀但字节不足（仅可能出现在流末端）
)

// Unit 是一次解码的结果：类型、码点（Bad/Short 时为 U+FFFD）、消费长度。
type Unit struct {
	Kind Kind
	R    rune
	N    int
	// Looked 为本单元解码实际检视到的字节数（含触发非法的终止字节）。
	Looked int
}

// DecodeAt 从 p[at:] 解码一个单元。
// available 为该位置之后（含该位置）可读到的字节数。
func DecodeAt(p []byte, at, available int) Unit {
	b := p[at]
	c, n := scalar.ClassifyU8(b)
	if c == scalar.U8Bad {
		return Unit{Kind: Bad, R: scalar.Replacement, N: 1, Looked: 1}
	}
	lim := at + n
	if at+n > at+available {
		lim = at + available
	}
	if at+1 >= lim {
		return Unit{Kind: Short, R: scalar.Replacement, N: lim - at, Looked: lim - at}
	}
	if !scalar.SecondOK(c, p[at+1]) {
		return Unit{Kind: Bad, R: scalar.Replacement, N: 1, Looked: 2}
	}
	for k := at + 2; k < lim; k++ {
		if !scalar.Cont(p[k]) {
			return Unit{Kind: Bad, R: scalar.Replacement, N: k - at, Looked: k - at + 1}
		}
	}
	if lim < at+n {
		return Unit{Kind: Short, R: scalar.Replacement, N: lim - at, Looked: lim - at}
	}
	return Unit{Kind: OK, R: decodeValue(p[at:lim], c), N: n, Looked: n}
}

func decodeValue(s []byte, c scalar.U8Class) rune {
	switch c {
	case scalar.U8Len2:
		return rune(s[0]&0x1F)<<6 | rune(s[1]&0x3F)
	case scalar.U8F0, scalar.U8FMid, scalar.U8F4:
		return rune(s[0]&0x07)<<18 | rune(s[1]&0x3F)<<12 |
			rune(s[2]&0x3F)<<6 | rune(s[3]&0x3F)
	default:
		return rune(s[0]&0x0F)<<12 | rune(s[1]&0x3F)<<6 | rune(s[2]&0x3F)
	}
}

// EncodeLen 返回标量的 UTF-8 编码字节数；非标量返回 0。
func EncodeLen(r rune) int {
	switch {
	case !scalar.Valid(r):
		return 0
	case r < 0x80:
		return 1
	case r < 0x800:
		return 2
	case r < 0x10000:
		return 3
	default:
		return 4
	}
}

// Encode 把标量追加编码到 dst，非法标量不追加。
func Encode(dst []byte, r rune) []byte {
	switch {
	case r < 0x80 && r >= 0:
		return append(dst, byte(r))
	case r < 0x800:
		return append(dst, 0xC0|byte(r>>6), 0x80|byte(r)&0x3F)
	case r < 0x10000 && scalar.Valid(r):
		return append(dst, 0xE0|byte(r>>12), 0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	case scalar.Valid(r):
		return append(dst, 0xF0|byte(r>>18), 0x80|byte(r>>12)&0x3F,
			0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	}
	return dst
}
