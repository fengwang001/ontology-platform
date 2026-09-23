// Package scalar 判定单个 Unicode 标量值的合法区间。
// 本包不依赖工程内其他包。
package scalar

// Rune 是一个 Unicode 码点（rune 别名，避免在接口处依赖隐式解码语义）。
type Rune = rune

// 区间边界（均手写常量，不引用 unicode 包）。
const (
	maxUnicode   = 0x10FFFF
	surrogateLo  = 0xD800
	surrogateHi  = 0xDFFF
	replacement  = 0xFFFD
	bom          = 0xFEFF
)

// IsScalar 报告 r 是否为 Unicode 标量值：0..0x10FFFF 且不在代理区。
func IsScalar(r Rune) bool {
	return uint32(r) <= maxUnicode && !IsSurrogate(r)
}

// IsSurrogate 报告 r 是否落在代理区 D800..DFFF。
func IsSurrogate(r Rune) bool {
	return uint32(r)-surrogateLo <= surrogateHi-surrogateLo
}

// IsHighSurrogate 报告 r 是否为高代理 D800..DBFF。
func IsHighSurrogate(r Rune) bool {
	return uint32(r)-surrogateLo <= 0xDBFF-surrogateLo
}

// IsLowSurrogate 报告 r 是否为低代理 DC00..DFFF。
func IsLowSurrogate(r Rune) bool {
	return uint32(r)-0xDC00 <= surrogateHi-0xDC00
}

// Replacement 返回替换字符 U+FFFD。
func Replacement() Rune { return replacement }

// BOM 返回字节序标记 U+FEFF。
func BOM() Rune { return bom }

// FromSurrogatePair 把一对高/低代理组合成码点；调用方须自行保证代理合法。
func FromSurrogatePair(hi, lo Rune) Rune {
	return 0x10000 + ((hi-surrogateLo)<<10) | (lo - 0xDC00)
}
