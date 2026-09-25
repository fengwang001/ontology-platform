// Package u8 在字节级实现 UTF-8 的逐标量解码与编码，依赖 scalar。
// 不使用 unicode/utf8 及任何隐式字符串解码。
package u8

import "ontology/scalar"

// Unit 描述一次解码结果。OK 为真时 R 是合法标量、Len 是其长度；
// OK 为假时 Len 是本次非法单元吞掉的字节数；
// Len==0 表示数据不足，调用方须等待更多字节（或在流结束时判截断）。
type Unit struct {
	R   rune
	Len int
	OK  bool
}

// First 从 p 的起点解码一个单元，语义见 Unit。
func First(p []byte) Unit {
	if len(p) == 0 {
		return Unit{}
	}
	c1 := p[0]
	n := scalar.LeadLen(c1)
	if n == 0 {
		return Unit{Len: 1} // 非法首字节：独立非法单元
	}
	if n == 1 {
		return Unit{R: rune(c1), Len: 1, OK: true}
	}
	if len(p) < n {
		return Unit{} // 合法前缀但数据不足
	}
	if !scalar.SecondOK(c1, p[1]) {
		return Unit{Len: 2} // 第二字节非法：吞 2，p[1] 由调用方重解析
	}
	r := rune(c1)
	for i := 1; i < n; i++ {
		c := p[i]
		if i >= 2 && !scalar.IsContinuation(c) {
			return Unit{Len: i + 1} // 第 i+1 字节位非法：吞到坏字节
		}
		r = r<<6 | rune(c&0x3F)
	}
	// 去掉首字节中的前导 1 与标志位：n 字节首字节有效位为 7-n 个。
	r &= 1<<uint(7-n+6*(n-1)) - 1
	return Unit{R: r, Len: n, OK: true}
}

// Encode 把标量 r 编码为 UTF-8，追加到 dst 后返回。非法标量按调用方约定处理。
func Encode(dst []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(dst, byte(r))
	case r < 0x800:
		return append(dst, 0xC0|byte(r>>6), 0x80|byte(r&0x3F))
	case r < 0x10000:
		return append(dst, 0xE0|byte(r>>12), 0x80|byte(r>>6)&0x3F, 0x80|byte(r&0x3F))
	default:
		return append(dst, 0xF0|byte(r>>18), 0x80|byte(r>>12)&0x3F,
			0x80|byte(r>>6)&0x3F, 0x80|byte(r&0x3F))
	}
}

// EncLen 返回 r 的 UTF-8 编码字节数。
func EncLen(r rune) int {
	switch {
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

// IsBOMStart 报告 b 是否可能是 UTF-8 BOM（EF BB BF）首字节。
func IsBOMStart(b byte) bool { return b == 0xEF }

// BOM 是 UTF-8 BOM 的字节序列。
var BOM = []byte{0xEF, 0xBB, 0xBF}
