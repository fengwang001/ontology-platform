// Package eval 实现 Horner 求值，全程 int64 精确运算并检出溢出。
package eval

import (
	"errors"
	"math/bits"
	"sync/atomic"

	"ontology/poly"
)

// 可判定的哨兵错误，互不相同。
var (
	ErrMulOverflow = errors.New("eval: multiplication overflow")
	ErrAddOverflow = errors.New("eval: addition overflow")
)

// lastOps 记录最近一次 Eval 的乘加次数。非导出，不出现在公开接口；
// 用 atomic 保证并发 Eval 下 go test -race 干净。仅供包内测试观测。
var lastOps atomic.Int64

// mul 返回 a*b，溢出时报 ErrMulOverflow。
func mul(a, b int64) (int64, error) {
	hi, lo := bits.Mul64(uint64(a), uint64(b))
	// 无符号 128 位积修正为符号积：a = uint64(a) - (a<0 ? 2^64 : 0)。
	if a < 0 {
		hi -= uint64(b)
	}
	if b < 0 {
		hi -= uint64(a)
	}
	// 能装进 int64 当且仅当高 64 位是低 64 位的符号扩展。
	if hi != uint64(int64(lo)>>63) {
		return 0, ErrMulOverflow
	}
	return int64(lo), nil
}

// add 返回 a+b，溢出时报 ErrAddOverflow。
func add(a, b int64) (int64, error) {
	s := a + b
	if (b > 0 && s < a) || (b < 0 && s > a) {
		return 0, ErrAddOverflow
	}
	return s, nil
}

// Eval 用 Horner 法计算 coeff[0] + coeff[1]*x + ... + coeff[n-1]*x^(n-1)。
// 与 acc=0 起迭代等价地，取 acc=最高次系数，再迭代 len-1 次（首次 0*x+c 必不溢出），
// 故乘加次数恰为次数 m=len(coeff)-1。任一步溢出立即返回 0 与哨兵错误，不留半成品。
func Eval(p poly.Poly, x int64) (int64, error) {
	if err := p.Check(); err != nil {
		return 0, err
	}
	c := p.Coeffs()
	if len(c) == 0 {
		lastOps.Store(0)
		return 0, nil
	}
	acc := c[len(c)-1]
	var ops int64
	for i := len(c) - 2; i >= 0; i-- {
		prod, err := mul(acc, x)
		if err != nil {
			return 0, err
		}
		acc, err = add(prod, c[i])
		if err != nil {
			return 0, err
		}
		ops++
	}
	lastOps.Store(ops)
	return acc, nil
}
