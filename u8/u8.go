// Package u8 逐标量解码/编码 UTF-8，所有字节判定手写，不用 unicode/utf8。
package u8

import "ontology/scalar"

// Unit 是一次解码结果：Size 为本次消费字节数；Size==0 表示前缀未完成需等更多字节。
// Bad 为真表示一个非法单元（Rune==Replacement）。
type Unit struct {
	Rune rune
	Size int
	Bad  bool
}

// IsCont 报告 c 是否为 continuation 字节 10xxxxxx。
func IsCont(c byte) bool { return c&0xC0 == 0x80 }

// IsLead 报告 c 是否可能作为一个合法单元的首字节（ASCII 或 C2..F4）。
func IsLead(c byte) bool { return c < 0x80 || c >= 0xC2 }

// DecodeAt 解码 b 开头的一个单元，不修改 b。
func DecodeAt(b []byte) Unit {
	if len(b) == 0 {
		return Unit{}
	}
	c := b[0]
	if c < 0x80 {
		return Unit{Rune: rune(c), Size: 1}
	}
	n, lo, hi := leadSpec(c)
	if n == 0 {
		return bad(1)
	}
	if len(b) >= 2 && (!IsCont(b[1]) || b[1] < lo || b[1] > hi) {
		return bad(1) // 第二字节判定失败：只吞首字节
	}
	if len(b) < n {
		return Unit{} // 合法（或未判定）前缀，等待更多字节
	}
	r := rune(c & (0xFF >> uint(n+1)))
	for i := 1; i < n; i++ {
		if !IsCont(b[i]) {
			return bad(i) // 前缀合法后中途断开：吞掉整个已收前缀
		}
		r = r<<6 | rune(b[i]&0x3F)
	}
	if !scalar.IsScalar(r) {
		return bad(n)
	}
	return Unit{Rune: r, Size: n}
}

func leadSpec(c byte) (n int, lo, hi byte) {
	switch {
	case c >= 0xC2 && c <= 0xDF:
		return 2, 0x80, 0xBF
	case c == 0xE0:
		return 3, 0xA0, 0xBF
	case c >= 0xE1 && c <= 0xEC:
		return 3, 0x80, 0xBF
	case c == 0xED:
		return 3, 0x80, 0x9F
	case c >= 0xEE && c <= 0xEF:
		return 3, 0x80, 0xBF
	case c == 0xF0:
		return 4, 0x90, 0xBF
	case c >= 0xF1 && c <= 0xF3:
		return 4, 0x80, 0xBF
	case c == 0xF4:
		return 4, 0x80, 0x8F
	}
	return 0, 0, 0
}

func bad(size int) Unit { return Unit{Rune: scalar.Replacement, Size: size, Bad: true} }

// EncLen 返回标量 r 的 UTF-8 编码字节数。
func EncLen(r rune) int {
	switch {
	case r < 0x80:
		return 1
	case r < 0x800:
		return 2
	case r < 0x10000:
		return 3
	}
	return 4
}

// EncodeAppend 把标量 r 编码后追加到 dst。
func EncodeAppend(dst []byte, r rune) []byte {
	switch n := EncLen(r); {
	case n == 1:
		return append(dst, byte(r))
	case n == 2:
		return append(dst, 0xC0|byte(r>>6), 0x80|byte(r)&0x3F)
	case n == 3:
		return append(dst, 0xE0|byte(r>>12), 0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	}
	return append(dst, 0xF0|byte(r>>18), 0x80|byte(r>>12)&0x3F,
		0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
}

// Encode 返回标量 r 的 UTF-8 编码。
func Encode(r rune) []byte { return EncodeAppend(nil, r) }
