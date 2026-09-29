// Package scalar 判定单个 Unicode 标量值（scalar value）。
// 本包不依赖工程内任何其他包，且不使用 unicode/utf* 与隐式解码。
package scalar

// Rune 是一个 Unicode 码点的原始表示。
type Rune = rune

const (
	// MaxRune 是 Unicode 码点上限 U+10FFFF。
	MaxRune = 0x10FFFF
	// SurrogateMin 为高/低代理区起点 U+D800。
	SurrogateMin = 0xD800
	// SurrogateMax 为代理区终点 U+DFFF。
	SurrogateMax = 0xDFFF
	// Replacement 是替换字符 U+FFFD。
	Replacement = 0xFFFD
	// BOM 是字节序标记/零宽不换行空格 U+FEFF。
	BOM = 0xFEFF
)

// IsScalar 报告 r 是否为合法标量：未越界且不在代理区。
// 非最短形式（overlong）与超范围无法在单个码点上表达，
// 它们由 u8/u16 在字节组合阶段用 ContOK 之类规则拒绝。
func IsScalar(r Rune) bool {
	return r >= 0 && r <= MaxRune && !IsSurrogate(r)
}

// IsSurrogate 报告 r 是否落在代理区 U+D800..U+DFFF。
func IsSurrogate(r Rune) bool {
	return r >= SurrogateMin && r <= SurrogateMax
}

// IsHighSurrogate 报告 r 是否为高代理 U+D800..U+DBFF。
func IsHighSurrogate(r Rune) bool {
	return r >= 0xD800 && r <= 0xDBFF
}

// IsLowSurrogate 报告 r 是否为低代理 U+DC00..U+DFFF。
func IsLowSurrogate(r Rune) bool {
	return r >= 0xDC00 && r <= 0xDFFF
}

// InRange 报告 b 是否落在闭区间 [lo, hi] 内。
// 所有字节级区间判定统一走这里，避免手写比较出错。
func InRange(b, lo, hi byte) bool { return b >= lo && b <= hi }

// IsCont 报告 b 是否为 UTF-8 续字节 10xxxxxx。
func IsCont(b byte) bool { return b&0xC0 == 0x80 }

// SurrogatePair 把高、低代理组合成标量；仅在两者均合法时返回。
func SurrogatePair(hi, lo Rune) (Rune, bool) {
	if !IsHighSurrogate(hi) || !IsLowSurrogate(lo) {
		return Replacement, false
	}
	return 0x10000 + (hi-0xD800)<<10 + (lo - 0xDC00), true
}

// SplitSurrogate 返回标量 r（≥ U+10000）的高、低代理。
func SplitSurrogate(r Rune) (hi, lo Rune) {
	r -= 0x10000
	return 0xD800 + r>>10, 0xDC00 + r&0x3FF
}
