// Package perfect 基于 sqrt 提供完美平方判定 IsSquare，并向上转发 Isqrt 与哨兵错误。
package perfect

import "ontology/sqrt"

// maxRoot 是 isqrt 在 int64 非负输入上的最大可能返回值：
// 3037000499^2 <= math.MaxInt64 < 3037000500^2，故 r <= maxRoot，
// r*r 不会溢出 int64（3037000499^2 = 9223372030926249001）。
const maxRoot = int64(3037000499)

// 哨兵错误由 sqrt 定义，这里原样别名转发，使 api 只依赖 perfect 一层。
var (
	ErrNegative     = sqrt.ErrNegative
	ErrMinInt       = sqrt.ErrMinInt
	ErrNotConverged = sqrt.ErrNotConverged
)

// Isqrt 转发整数平方根（向下取整）。
func Isqrt(n int64) (int64, error) { return sqrt.Isqrt(n) }

// IsSquare 判定 n 是否恰为某整数的平方：先 isqrt 再回乘核验。
// 回乘防溢出：r 恒有 r <= maxRoot，r*r 不溢出；r==0 时 n==0 必为平方。
func IsSquare(n int64) (bool, error) {
	r, err := sqrt.Isqrt(n)
	if err != nil {
		return false, err
	}
	if r > maxRoot {
		return false, nil // 防御：不可能到达，到达则必非平方
	}
	return r*r == n, nil
}
