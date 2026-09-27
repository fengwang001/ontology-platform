// Package scalar 判定单个 Unicode 标量值：合法区间、代理区、越界。
package scalar

const (
	maxRune  = '\U0010FFFF'
	surrLo   = '\uD800'
	surrHi   = '\uDFFF'
)

// IsScalar 报告 r 是否为 Unicode 标量值（非代理、不越界）。
func IsScalar(r rune) bool {
	return r >= 0 && r <= maxRune && !IsSurrogate(r)
}

// IsSurrogate 报告 r 是否落在 UTF-16 代理区 D800..DFFF。
func IsSurrogate(r rune) bool {
	return r >= surrLo && r <= surrHi
}
