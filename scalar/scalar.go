package scalar

type Rune = rune

const MaxRune = 0x10FFFF
const SurrogateMin = 0xD800
const SurrogateMax = 0xDFFF
const HighSurrogateMin = 0xD800
const HighSurrogateMax = 0xDBFF
const LowSurrogateMin = 0xDC00
const LowSurrogateMax = 0xDFFF
const ReplacementRune = 0xFFFD

// IsScalar 判断码点是否为合法 Unicode 标量值：非负、不越界、非代理区。
func IsScalar(r Rune) bool {
	return r >= 0 && r <= MaxRune && !IsSurrogate(r)
}

// IsSurrogate 判断码点是否落入 UTF-16 代理区。
func IsSurrogate(r Rune) bool { return r >= SurrogateMin && r <= SurrogateMax }

// IsHighSurrogate 判断是否为高代理（前导代理）码元。
func IsHighSurrogate(r Rune) bool { return r >= HighSurrogateMin && r <= HighSurrogateMax }

// IsLowSurrogate 判断是否为低代理（尾随后代理）码元。
func IsLowSurrogate(r Rune) bool { return r >= LowSurrogateMin && r <= LowSurrogateMax }

// DecodeSurrogatePair 把高、低代理还原为标量；入参非法时返回 false。
func DecodeSurrogatePair(hi, lo Rune) (rune, bool) {
	if !IsHighSurrogate(hi) || !IsLowSurrogate(lo) {
		return 0, false
	}
	return 0x10000 + (hi-HighSurrogateMin)<<10 + (lo - LowSurrogateMin), true
}

// EncodeSurrogatePair 把超 BMP 标量编成 (高代理, 低代理)；非法时返回 false。
func EncodeSurrogatePair(r Rune) (hi, lo Rune, ok bool) {
	if r < 0x10000 || r > MaxRune {
		return 0, 0, false
	}
	r -= 0x10000
	return HighSurrogateMin + r>>10, LowSurrogateMin + r&0x3FF, true
}
