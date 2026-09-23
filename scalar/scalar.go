package scalar

// 本包只做单个 Unicode 标量值（scalar value）的判定，
// 不依赖工程内任何其他包，也不使用 unicode/utf8、unicode/utf16。

// RuneMax 是 Unicode 标量值的上界（含）。
const RuneMax = 0x10FFFF

// 代理区 [SurrogateMin, SurrogateMax] 不属于标量值。
const (
	SurrogateMin = 0xD800
	SurrogateMax = 0xDFFF
)

// Replacement 是 U+FFFD 替换字符。
const Replacement = 0xFFFD

// BOM 是 U+FEFF。
const BOM = 0xFEFF

// IsScalar 判断 r 是否为合法 Unicode 标量值：
// 非负、不越界、不落在代理区。
func IsScalar(r rune) bool {
	if r < 0 || r > RuneMax {
		return false
	}
	if r >= SurrogateMin && r <= SurrogateMax {
		return false
	}
	return true
}

// IsSurrogate 判断 r 是否为 UTF-16 代理码元。
func IsSurrogate(r rune) bool {
	return r >= SurrogateMin && r <= SurrogateMax
}

// IsHighSurrogate 判断 r 是否为高代理（前导代理）D800..DBFF。
func IsHighSurrogate(r rune) bool {
	return r >= 0xD800 && r <= 0xDBFF
}

// IsLowSurrogate 判断 r 是否为低代理（尾随代理）DC00..DFFF。
func IsLowSurrogate(r rune) bool {
	return r >= 0xDC00 && r <= 0xDFFF
}

// SurrogatePair 把高代理 hi 与低代理 lo 组合成标量值。
// 调用方需自行保证 hi、lo 分别是高、低代理。
func SurrogatePair(hi, lo rune) rune {
	return 0x10000 + (hi-0xD800)<<10 + (lo - 0xDC00)
}

// SplitSurrogate 把 >= 0x10000 的标量拆成高、低代理码元。
func SplitSurrogate(r rune) (hi, lo rune) {
	r -= 0x10000
	return 0xD800 + r>>10, 0xDC00 + r&0x3FF
}
