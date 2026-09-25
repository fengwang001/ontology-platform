// Package num 提供 gcd（对绝对值做欧几里得）、符号归一、
// 带溢出检出的 int64 运算。不依赖其他包。
package num

import (
	"errors"
	"math"
	"math/big"
	"sync/atomic"
)

// 可判定的哨兵错误，三者互不相同。
var (
	ErrZeroDenominator = errors.New("num: denominator is zero")
	ErrMulOverflow     = errors.New("num: multiplication overflow")
	ErrAddOverflow     = errors.New("num: addition overflow")
	ErrNormOverflow    = errors.New("num: normalization overflow")
)

// lastSteps 记录最近一次 gcd 的欧几里得迭代步数。
// 非导出，不出现在任何公开接口；用 atomic 保证并发读写无数据竞争。
var lastSteps atomic.Int64

// absU 返回 |a| 的 uint64 表示，MinInt64 也不会溢出。
func absU(a int64) uint64 {
	if a < 0 {
		return uint64(-(a + 1)) + 1
	}
	return uint64(a)
}

// gcdU 对非负整数做欧几里得算法，返回 gcd 与迭代步数。
func gcdU(a, b uint64) (uint64, int64) {
	var steps int64
	for b != 0 {
		a, b = b, a%b
		steps++
	}
	return a, steps
}

// Gcd 返回 |a| 与 |b| 的最大公约数。
// 调用方须保证结果可表示为 int64（即非 a==b==math.MinInt64）。
func Gcd(a, b int64) int64 {
	g, steps := gcdU(absU(a), absU(b))
	lastSteps.Store(steps)
	return int64(g)
}

// GcdBig 对大整数做欧几里得 gcd，用于验证迭代步数随位数线性增长。
func GcdBig(a, b *big.Int) *big.Int {
	x := new(big.Int).Abs(a)
	y := new(big.Int).Abs(b)
	var steps int64
	for y.Sign() != 0 {
		m := new(big.Int).Mod(x, y)
		x, y = y, m
		steps++
	}
	lastSteps.Store(steps)
	return x
}

// Normalize 把 n/d 约分为既约形式并把符号归到分子（d 恒正，0 → 0/1）。
// d == 0 报 ErrZeroDenominator；既约分母或正分子为 2^63 无法表示时报
// ErrNormOverflow。失败不留下任何状态。
func Normalize(n, d int64) (int64, int64, error) {
	if d == 0 {
		return 0, 0, ErrZeroDenominator
	}
	if n == 0 {
		return 0, 1, nil
	}
	ua, ub := absU(n), absU(d)
	g, steps := gcdU(ua, ub)
	lastSteps.Store(steps)
	mn, md := ua/g, ub/g
	if md > math.MaxInt64 { // 既约分母为 2^63，int64 无法表示
		return 0, 0, ErrNormOverflow
	}
	neg := (n < 0) != (d < 0)
	if mn > math.MaxInt64 { // 分子绝对值为 2^63，仅负值可表示
		if !neg {
			return 0, 0, ErrNormOverflow
		}
		return math.MinInt64, int64(md), nil
	}
	rn := int64(mn)
	if neg {
		rn = -rn
	}
	return rn, int64(md), nil
}

// Mul64 返回 a*b，溢出时报 ErrMulOverflow，绝不静默回绕。
func Mul64(a, b int64) (int64, error) {
	if a == 0 || b == 0 {
		return 0, nil
	}
	r := a * b
	if r/a != b || (a == -1 && b == math.MinInt64) || (b == -1 && a == math.MinInt64) {
		return 0, ErrMulOverflow
	}
	return r, nil
}

// Add64 返回 a+b，溢出时报 ErrAddOverflow。
func Add64(a, b int64) (int64, error) {
	r := a + b
	if (b > 0 && r < a) || (b < 0 && r > a) {
		return 0, ErrAddOverflow
	}
	return r, nil
}

// Sub64 返回 a-b，溢出时报 ErrAddOverflow。
func Sub64(a, b int64) (int64, error) {
	r := a - b
	if (b > 0 && r > a) || (b < 0 && r < a) {
		return 0, ErrAddOverflow
	}
	return r, nil
}
