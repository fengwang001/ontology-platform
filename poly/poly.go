// Package poly 定义多项式系数切片与小端约定：coeff[i] 是 x^i 的系数。
package poly

// MaxCoeffs 是系数个数上限（含），超过即次数超限。
const MaxCoeffs = 1 << 20

// Coeffs 是多项式系数，小端存放：Coeffs[i] 为 x^i 的系数。
// 空切片表示零多项式。
type Coeffs []int64

// Degree 返回多项式次数：len(coeff)-1；零多项式（空切片）为 -1。
func Degree(coeff Coeffs) int {
	return len(coeff) - 1
}

// Clone 返回系数的独立副本，保证构造后不被外部修改。
func Clone(coeff Coeffs) Coeffs {
	out := make(Coeffs, len(coeff))
	copy(out, coeff)
	return out
}
