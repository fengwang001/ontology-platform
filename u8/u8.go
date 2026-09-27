// Package u8 在字节级实现 UTF-8 的逐标量解码与编码。
package u8

import "ontology/scalar"

// Unit 是一次解码结果。
type Unit struct {
	R       rune
	Size    int  // 本单元占用输入字节数（合法或非法均 >0）
	Valid   bool // 合法标量
	Partial bool // 前缀合法但输入在此截断（Size=已收前缀长度）
	BOM     bool // 位于输入开头的 U+FEFF
}

const bom0, bom1, bom2 = 0xEF, 0xBB, 0xBF

// HasBOM 报告 p 是否以 UTF-8 BOM 开头。
func HasBOM(p []byte) bool {
	return len(p) >= 3 && p[0] == bom0 && p[1] == bom1 && p[2] == bom2
}

// DecodeOne 从 p 开头解码一个单元。
func DecodeOne(p []byte) Unit {
	if len(p) == 0 {
		return Unit{}
	}
	b0 := p[0]
	if b0 < 0x80 {
		return Unit{R: rune(b0), Size: 1, Valid: true}
	}
	n := scalar.SeqLen(b0)
	if n == 0 { // 孤立续字节 / C0 C1 F5..FF
		return Unit{R: scalar.Surrogate, Size: 1}
	}
	if len(p) < 2 {
		return Unit{R: scalar.Surrogate, Size: len(p), Partial: true}
	}
	if !scalar.SecondOK(b0, p[1]) {
		return Unit{R: scalar.Surrogate, Size: 1}
	}
	if len(p) < n {
		return Unit{R: scalar.Surrogate, Size: len(p), Partial: true}
	}
	var r rune
	switch n {
	case 2:
		r = rune(b0&0x1F)<<6 | rune(p[1]&0x3F)
	case 3:
		r = rune(b0&0x0F)<<12 | rune(p[1]&0x3F)<<6 | rune(p[2]&0x3F)
	case 4:
		r = rune(b0&0x07)<<18 | rune(p[1]&0x3F)<<12 |
			rune(p[2]&0x3F)<<6 | rune(p[3]&0x3F)
	}
	for i := 2; i < n; i++ {
		if !scalar.IsCont(p[i]) {
			return Unit{R: scalar.Surrogate, Size: i}
		}
	}
	if !scalar.IsScalar(r) {
		return Unit{R: scalar.Surrogate, Size: n}
	}
	u := Unit{R: r, Size: n, Valid: true}
	if r == 0xFEFF {
		u.BOM = true
	}
	return u
}

// EncLen 返回标量 r 的 UTF-8 编码长度。
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

// Append 把标量 r 编码追加到 dst。非法标量按 U+FFFD 编码。
func Append(dst []byte, r rune) []byte {
	if !scalar.IsScalar(r) {
		r = scalar.Surrogate
	}
	switch {
	case r < 0x80:
		return append(dst, byte(r))
	case r < 0x800:
		return append(dst, 0xC0|byte(r>>6), 0x80|byte(r)&0x3F)
	case r < 0x10000:
		return append(dst, 0xE0|byte(r>>12),
			0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	default:
		return append(dst, 0xF0|byte(r>>18),
			0x80|byte(r>>12)&0x3F, 0x80|byte(r>>6)&0x3F, 0x80|byte(r)&0x3F)
	}
}
