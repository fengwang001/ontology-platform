// Package scalar 判定单个 Unicode 标量值的合法性。
// 不依赖其他包，不使用 unicode/utf8、unicode/utf16。
package scalar

// Replacement 是非法单元的替换字符 U+FFFD。
const Replacement = 0xFFFD

const (
	maxScalar     = 0x10FFFF
	surrogateLo   = 0xD800
	surrogateHi   = 0xDFFF
	lowSurrogate  = 0xDC00
	highSurrogate = 0xDBFF
	supplementary = 0x10000
)

// Valid 报告 cp 是否为合法 Unicode 标量值（排除代理区与越界值）。
func Valid(cp int32) bool {
	return cp >= 0 && cp <= maxScalar && !(cp >= surrogateLo && cp <= surrogateHi)
}

// IsHighSurrogate 报告 u 是否为 UTF-16 高代理码元。
func IsHighSurrogate(u uint16) bool { return u >= surrogateLo && u <= highSurrogate }

// IsLowSurrogate 报告 u 是否为 UTF-16 低代理码元。
func IsLowSurrogate(u uint16) bool { return u >= lowSurrogate && u <= surrogateHi }

// IsSurrogate 报告 u 是否落在代理区。
func IsSurrogate(u uint16) bool { return u >= surrogateLo && u <= surrogateHi }

// CombineSurrogates 把一对代理合成标量值；调用方须保证参数合法。
func CombineSurrogates(hi, lo uint16) int32 {
	return supplementary + (int32(hi)-surrogateLo)<<10 + (int32(lo) - lowSurrogate)
}

// SplitSurrogates 把 >= U+10000 的标量拆成代理对；调用方须保证 cp 合法。
func SplitSurrogates(cp int32) (hi, lo uint16) {
	v := cp - supplementary
	return surrogateLo + uint16(v>>10), lowSurrogate + uint16(v&0x3FF)
}

// NeedsSurrogatePair 报告 cp 编码为 UTF-16 时是否需要代理对。
func NeedsSurrogatePair(cp int32) bool { return cp >= supplementary }
