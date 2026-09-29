package scalar

// Rune 是一个 Unicode 码位（未必是标量值）。
type Rune = rune

const (
	// MaxRune 是 Unicode 最大码位。
	MaxRune = 0x10FFFF
	// ReplacementChar 是 U+FFFD。
	ReplacementChar = '\uFFFD'
	// BOM 是 U+FEFF。
	BOM = '\uFEFF'
	surrLo = 0xD800
	surrHi = 0xDFFF
)

// IsScalar 判定 r 是否为 Unicode 标量值：
// 在范围内且不落在代理区 D800..DFFF。
func IsScalar(r rune) bool {
	return r >= 0 && r <= MaxRune && !IsSurrogate(r)
}

// IsSurrogate 判定 r 是否落在代理区。
func IsSurrogate(r rune) bool {
	return uint32(r)-surrLo <= surrHi-surrLo
}

// IsHighSurrogate 判定 r 是否为高代理 D800..DBFF。
func IsHighSurrogate(r rune) bool {
	return uint32(r)-surrLo <= 0x3FF
}

// IsLowSurrogate 判定 r 是否为低代理 DC00..DFFF。
func IsLowSurrogate(r rune) bool {
	return uint32(r)-0xDC00 <= 0x3FF
}

// SurrogatePair 把高、低代理组合成标量；输入非法时返回 ok=false。
func SurrogatePair(high, low rune) (rune, bool) {
	if !IsHighSurrogate(high) || !IsLowSurrogate(low) {
		return ReplacementChar, false
	}
	return 0x10000 + (high-surrLo)<<10 + (low - 0xDC00), true
}

// SplitSurrogate 把 r（≥10000）拆成高、低代理。
func SplitSurrogate(r rune) (high, low rune) {
	r -= 0x10000
	return surrLo + r>>10, 0xDC00 + r&0x3FF
}
