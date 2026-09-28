// Package scalar 提供单个 Unicode 标量值的字节无关判定原语。
package scalar

// Rune 是一个待判定的 Unicode 码点。
type Rune = rune

const (
	maxRune   = 0x10FFFF
	surrHigh  = 0xD800
	surrLow   = 0xDC00
	surrEnd   = 0xDFFF
	Replacement = 0xFFFD
	BOM       = 0xFEFF
)

// Valid 判定 r 是否为合法 Unicode 标量值（非代理、未越界）。
func Valid(r Rune) bool {
	return r >= 0 && r <= maxRune && !(r >= surrHigh && r <= surrEnd)
}

// IsSurrogate 判定 r 是否落在代理区 D800..DFFF。
func IsSurrogate(r Rune) bool { return r >= surrHigh && r <= surrEnd }

// HighSurrogate 判定 r 是否为高代理 D800..DBFF。
func HighSurrogate(r Rune) bool { return r >= surrHigh && r < surrLow }

// LowSurrogate 判定 r 是否为低代理 DC00..DFFF。
func LowSurrogate(r Rune) bool { return r >= surrLow && r <= surrEnd }

// FromSurrogatePair 由高、低代理还原码点；调用方须自行保证代理合法。
func FromSurrogatePair(hi, lo Rune) Rune {
	return 0x10000 + (hi-surrHigh)<<10 + (lo - surrLow)
}

// SurrogatePair 把 r（≥10000）拆成高、低代理。
func SurrogatePair(r Rune) (hi, lo Rune) {
	v := r - 0x10000
	return surrHigh + v>>10, surrLow + v&0x3FF
}

// Continuation 判定 b 是否为 UTF-8 续字节 10xxxxxx。
func Continuation(b byte) bool { return b&0xC0 == 0x80 }

// SecondByteOK 判定首字节 lead 与第二字节 b 的组合是否合法：
// 同时排除非最短形式（E0、F0）与越界（ED、F4）。
func SecondByteOK(lead, b byte) bool {
	if !Continuation(b) {
		return false
	}
	switch {
	case lead == 0xE0:
		return b >= 0xA0
	case lead == 0xED:
		return b <= 0x9F
	case lead == 0xF0:
		return b >= 0x90
	case lead == 0xF4:
		return b <= 0x8F
	default:
		return true
	}
}
