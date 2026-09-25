// Package eval 实现 Horner 求值：全程 int64 精确运算，乘法与加法各自检出溢出。
package eval

import (
	"errors"
	"math/bits"
	"sync/atomic"

	"ontology/poly"
)

// 三类互不相同、可判定的哨兵错误。
var (
	ErrMulOverflow = errors.New("eval: multiplication overflow")
	ErrAddOverflow = errors.New("eval: addition overflow")
	ErrDegreeLimit = errors.New("eval: coefficient count exceeds limit")
)

// lastOps 记录最近一次 Eval 的乘加次数。非导出，公开接口读不到它的数值。
var lastOps atomic.Int64

// mul 返回 a*b；结果超出 int64 时报 ErrMulOverflow，不返回半成品。
func mul(a, b int64) (int64, error) {
	uhi, lo := bits.Mul64(uint64(a), uint64(b))
	hi := int64(uhi)
	if a < 0 {
		hi -= b
	}
	if b < 0 {
		hi -= a
	}
	if hi != int64(lo)>>63 {
		return 0, ErrMulOverflow
	}
	return int64(lo), nil
}

// add 返回 a+b；结果超出 int64 时报 ErrAddOverflow，不返回半成品。
func add(a, b int64) (int64, error) {
	r := a + b
	if (a^r)&(b^r) < 0 {
		return 0, ErrAddOverflow
	}
	return r, nil
}

// Eval 用 Horner 法计算 Σ coeff[i]·x^i。系数小端存放（coeff[i] 是 x^i 的系数）。
// 空切片（零多项式）返回 0。len(coeff) 超过 poly.MaxCoeffs 时报 ErrDegreeLimit。
// 任一步乘法或加法溢出即返回对应错误，不返回半成品、不 panic。
func Eval(coeff []int64, x int64) (int64, error) {
	if len(coeff) > poly.MaxCoeffs {
		return 0, ErrDegreeLimit
	}
	if len(coeff) == 0 {
		lastOps.Store(0)
		return 0, nil
	}
	acc := coeff[len(coeff)-1]
	ops := int64(0)
	for i := len(coeff) - 2; i >= 0; i-- {
		p, err := mul(acc, x)
		if err != nil {
			return 0, err
		}
		s, err := add(p, coeff[i])
		if err != nil {
			return 0, err
		}
		acc = s
		ops++
	}
	lastOps.Store(ops)
	return acc, nil
}

// SelfCheck 内部核验乘加次数随次数线性（次数 m 时恰为 m 次），
// 只返回判定结果，不暴露计数器数值。
func SelfCheck() bool {
	for _, m := range []int{100, 1000, 10000} {
		coeff := make([]int64, m+1)
		for i := range coeff {
			coeff[i] = 1
		}
		if _, err := Eval(coeff, 0); err != nil {
			return false
		}
		if lastOps.Load() != int64(m) {
			return false
		}
	}
	return true
}
