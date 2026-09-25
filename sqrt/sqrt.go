// Package sqrt 提供整数平方根 isqrt（向下取整），全程整数运算，不用 float64。
package sqrt

import (
	"errors"
	"math"
	"math/bits"
	"sync/atomic"
)

// 三类可判定哨兵错误，互不相同。
var (
	ErrNegative     = errors.New("sqrt: negative input")
	ErrMinInt       = errors.New("sqrt: math.MinInt64 input, negation overflows int64")
	ErrNotConverged = errors.New("sqrt: newton iteration did not converge within 128 steps")
)

// maxIters 是防御性上限；正常牛顿迭代个位数步内收敛。
const maxIters = 128

// lastIters 记录最近一次 isqrt 的牛顿迭代次数。
// 非导出，不出现在任何公开接口；用 atomic 保证并发调用 -race 干净。
var lastIters atomic.Int64

// newton 是单次求值的迭代器状态。
type newton struct{ iters int }

// isqrt 返回最大的 r 使 r*r <= n。初值 x 取 2^ceil(bitlen/2) >= sqrt(n)，
// 迭代 y=(x+n/x)/2 单调下降，y>=x 时 x 即所求。比较与除法均不溢出：
// x >= sqrt(n) 时 n/x <= x，故 x+n/x <= 2x <= 2^33。
func (w *newton) isqrt(n int64) (int64, error) {
	if n == math.MinInt64 {
		return 0, ErrMinInt
	}
	if n < 0 {
		return 0, ErrNegative
	}
	if n < 2 {
		return n, nil
	}
	x := int64(1) << uint((bits.Len64(uint64(n))+1)/2)
	for {
		w.iters++
		if w.iters > maxIters {
			return 0, ErrNotConverged
		}
		y := (x + n/x) / 2
		if y >= x {
			return x, nil
		}
		x = y
	}
}

// Isqrt 返回最大的 r 使 r*r <= n；n<0 报 ErrNegative，n==math.MinInt64 报 ErrMinInt。
func Isqrt(n int64) (int64, error) {
	var w newton
	r, err := w.isqrt(n)
	lastIters.Store(int64(w.iters))
	return r, err
}
