// Package scalar 判定单个 Unicode 标量值与 UTF-16 代理码元。
package scalar

const (
	MaxRune      = '\U0010FFFF'
	Replacement  = '\uFFFD'
	SurrogateMin = 0xD800
	SurrogateMax = 0xDFFF
	HighMin      = 0xD800
	HighMax      = 0xDBFF
	LowMin       = 0xDC00
	LowMax       = 0xDFFF
)

// IsScalar 判断 r 是否为合法 Unicode 标量值（排除代理区与越界值）。
func IsScalar(r rune) bool {
	return r >= 0 && r <= MaxRune && !(r >= SurrogateMin && r <= SurrogateMax)
}

// IsHigh 判断 16 位码元是否为高代理。
func IsHigh(u uint16) bool { return u >= HighMin && u <= HighMax }

// IsLow 判断 16 位码元是否为低代理。
func IsLow(u uint16) bool { return u >= LowMin && u <= LowMax }

// IsSurrogate 判断 16 位码元是否落在代理区。
func IsSurrogate(u uint16) bool { return u >= SurrogateMin && u <= SurrogateMax }

// JoinPair 组合一对代理为标量值。
func JoinPair(hi, lo uint16) rune {
	return rune(uint32(hi-HighMin)<<10|uint32(lo-LowMin)) + 0x10000
}
