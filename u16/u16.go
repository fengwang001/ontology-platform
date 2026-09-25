package u16

import "ontology/scalar"

type Endian int

const (
	LE Endian = iota
	BE
)

const MaxPending = 2

// Unit 表示一次解码：Size 为消费字节数（2 或 4），Bad 为孤立代理等。
type Unit struct {
	Size int
	Bad  bool
	// Lone 为 true 表示一个孤立高/低代理（Size==2），高代理后非低代理时
	// 只消费高代理本身，下个码元重新解析。
	Lone  bool
	Value rune
}

// DecodeOne 从 p 解码一个 UTF-16 码元/代理对；need>0 表示字节不足。
func DecodeOne(p []byte, e Endian) (u Unit, need int) {
	if len(p) < 2 {
		return Unit{}, 2 - len(p)
	}
	first := codeUnit(p[0], p[1], e)
	switch {
	case scalar.IsHighSurrogate(rune(first)):
		if len(p) < 4 {
			return Unit{}, 2 // 高代理后可能还有低代理，等待
		}
		second := codeUnit(p[2], p[3], e)
		if scalar.IsLowSurrogate(rune(second)) {
			r, _ := scalar.DecodeSurrogatePair(rune(first), rune(second))
			return Unit{Size: 4, Value: r}, 0
		}
		// 高代理后跟非低代理：只吞高代理，下个码元重新解析。
		return Unit{Size: 2, Bad: true, Lone: true, Value: 0xFFFD}, 0
	case scalar.IsLowSurrogate(rune(first)):
		return Unit{Size: 2, Bad: true, Lone: true, Value: 0xFFFD}, 0
	}
	return Unit{Size: 2, Value: rune(first)}, 0
}

func codeUnit(hi, lo byte, e Endian) uint16 {
	if e == BE {
		return uint16(hi)<<8 | uint16(lo)
	}
	return uint16(lo)<<8 | uint16(hi)
}

func emitUnit(dst []byte, cu uint16, e Endian) []byte {
	if e == BE {
		return append(dst, byte(cu>>8), byte(cu))
	}
	return append(dst, byte(cu), byte(cu>>8))
}

// EncodeRune 以指定字节序把标量追加为 UTF-16（BMP 直接，超 BMP 代理对）。
func EncodeRune(dst []byte, r rune, e Endian) []byte {
	if hi, lo, ok := scalar.EncodeSurrogatePair(r); ok {
		dst = emitUnit(dst, uint16(hi), e)
		return emitUnit(dst, uint16(lo), e)
	}
	return emitUnit(dst, uint16(r), e)
}

// Boundary 对齐到偶偏移并处理代理对归属（回看 ≤2 字节）。
func Boundary(b []byte, cut int, e Endian) int {
	if cut <= 0 {
		return 0
	}
	if cut >= len(b) {
		return len(b)
	}
	if cut%2 == 1 {
		cut++ // 跨切点的码元归上段
	}
	if cut >= 2 && cut <= len(b) {
		if u, _ := DecodeOne(b[cut-2:], e); u.Size == 4 {
			cut += 2 // 代理对跨切点，首码元在上段
		}
	}
	if cut > len(b) {
		return len(b)
	}
	return cut
}
