// Package scalar 判定单个 Unicode 标量值。
package scalar

// Max 是 Unicode 标量值上界。
const Max = 0x10FFFF

const (
	surrogateLo = 0xD800
	surrogateHi = 0xDFFF
)

// IsScalar 报告 r 是否为合法 Unicode 标量值（非代理、不越界）。
func IsScalar(r rune) bool {
	return r >= 0 && r <= Max && (r < surrogateLo || r > surrogateHi)
}

// IsSurrogate 报告 r 是否落在代理区 D800..DFFF。
func IsSurrogate(r rune) bool { return r >= surrogateLo && r <= surrogateHi }

// IsHighSurrogate 报告 r 是否为高代理 D800..DBFF。
func IsHighSurrogate(r rune) bool { return r >= surrogateLo && r <= 0xDBFF }

// IsLowSurrogate 报告 r 是否为低代理 DC00..DFFF。
func IsLowSurrogate(r rune) bool { return r >= 0xDC00 && r <= surrogateHi }

// FromSurrogates 把高、低代理组合成标量；输入非法时返回 ok=false。
func FromSurrogates(hi, lo rune) (rune, bool) {
	if !IsHighSurrogate(hi) || !IsLowSurrogate(lo) {
		return 0, false
	}
	return 0x10000 + (hi-surrogateLo)<<10 + (lo - 0xDC00), true
}
