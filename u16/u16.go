// Package u16 在字节级实现 UTF-16LE/BE 的解码与编码。
package u16

import "ontology/scalar"

type Order int

const (
	LE Order = iota
	BE
)

// Unit 是一次解码结果（一个 16 位单元或一对代理）。
type Unit struct {
	R       rune
	Size    int  // 占用输入字节数：1(截断字节) 2 或 4
	Valid   bool // 合法标量
	Bad     bool // 非法单元（孤立/错配代理）
	Partial bool // 高代理后流结束，或仅剩 1 字节
	BOM     bool // 流开头的 U+FEFF
}

// DetectBOM 判断开头 BOM；返回字节序、是否存在 BOM。
func DetectBOM(p []byte) (Order, bool) {
	if len(p) < 2 {
		return LE, false
	}
	if p[0] == 0xFF && p[1] == 0xFE {
		return LE, true
	}
	if p[0] == 0xFE && p[1] == 0xFF {
		return BE, true
	}
	return LE, false
}

func join(hi, lo rune) rune {
	return 0x10000 + (hi-0xD800)<<10 + (lo - 0xDC00)
}

// DecodeOne 从 p 开头解码一个单元，o 指定字节序。
func DecodeOne(p []byte, o Order) Unit {
	if len(p) == 0 {
		return Unit{}
	}
	if len(p) == 1 {
		return Unit{R: scalar.Surrogate, Size: 1, Partial: true}
	}
	u := uint16(p[0])<<8 | uint16(p[1])
	if o == LE {
		u = uint16(p[1])<<8 | uint16(p[0])
	}
	r := rune(u)
	if scalar.IsHighSurrogate(r) {
		if len(p) < 4 {
			return Unit{R: scalar.Surrogate, Size: 2, Partial: true}
		}
		u2 := uint16(p[2])<<8 | uint16(p[3])
		if o == LE {
			u2 = uint16(p[3])<<8 | uint16(p[2])
		}
		r2 := rune(u2)
		if scalar.IsLowSurrogate(r2) {
			return Unit{R: join(r, r2), Size: 4, Valid: true}
		}
		return Unit{R: scalar.Surrogate, Size: 2, Bad: true}
	}
	if scalar.IsLowSurrogate(r) {
		return Unit{R: scalar.Surrogate, Size: 2, Bad: true}
	}
	u0 := Unit{R: r, Size: 2, Valid: true}
	if r == 0xFEFF {
		u0.BOM = true
	}
	return u0
}

// EncLen 返回标量 r 的 UTF-16 编码单元数（2 或 4 字节）。
func EncLen(r rune) int {
	if r >= 0x10000 {
		return 4
	}
	return 2
}

// Append 把标量 r 编码追加到 dst，非法标量按 U+FFFD 编码。
func Append(dst []byte, r rune, o Order) []byte {
	if !scalar.IsScalar(r) {
		r = scalar.Surrogate
	}
	put := func(u uint16) []byte {
		if o == LE {
			return append(dst, byte(u), byte(u>>8))
		}
		return append(dst, byte(u>>8), byte(u))
	}
	if r < 0x10000 {
		return put(uint16(r))
	}
	r -= 0x10000
	dst = put(uint16(0xD800 + r>>10))
	return put(uint16(0xDC00 + r&0x3FF))
}
