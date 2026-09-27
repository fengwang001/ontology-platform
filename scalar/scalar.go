package scalar

// Rune 是一个 21 位 Unicode 码点；非法码点用 Valid 区分。
type Rune struct {
	Value rune
	Valid bool
}

const (
	MaxRune   = 0x10FFFF
	SurHighLo = 0xD800
	SurHighHi = 0xDBFF
	SurLowLo  = 0xDC00
	SurLowHi  = 0xDFFF
	Replacement = 0xFFFD
)

// Scalar 判断 r 是否为标量值（非代理、未越界）。
func Scalar(r rune) bool { return r >= 0 && r <= MaxRune && !Surrogate(r) }

// Surrogate 判断 r 是否落在代理区 D800..DFFF。
func Surrogate(r rune) bool { return r >= SurHighLo && r <= SurLowHi }

// HighSurrogate 判断 r 是否为高代理 D800..DBFF。
func HighSurrogate(r rune) bool { return r >= SurHighLo && r <= SurHighHi }

// LowSurrogate 判断 r 是否为低代理 DC00..DFFF。
func LowSurrogate(r rune) bool { return r >= SurLowLo && r <= SurLowHi }

// FromSurrogatePair 由高/低代理还原码点。
func FromSurrogatePair(hi, lo rune) rune {
	return 0x10000 + (hi-SurHighLo)<<10 + (lo - SurLowLo)
}

// ToSurrogatePair 把 r(>=0x10000) 拆成高、低代理。
func ToSurrogatePair(r rune) (hi, lo rune) {
	r -= 0x10000
	return SurHighLo + r>>10, SurLowLo + r&0x3FF
}
