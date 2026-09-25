// Package api 是对外门面：New / Isqrt / IsSquare / SelfCheck。
// 依赖方向 api → perfect → sqrt，反向依赖不存在。
package api

import (
	"errors"
	"fmt"
	"math"

	"ontology/perfect"
)

// API 是无状态门面；零值不可用，请用 New 构造。
type API struct{}

// New 返回一个可用的 API。方法均为无状态纯函数，可并发调用。
func New() *API { return &API{} }

// Isqrt 返回最大的 r 使 r*r <= n。
func (a *API) Isqrt(n int64) (int64, error) { return perfect.Isqrt(n) }

// IsSquare 判定 n 是否恰为某整数的平方。
func (a *API) IsSquare(n int64) (bool, error) { return perfect.IsSquare(n) }

// checkN 是自检内置样本：覆盖 0/1、八行表中的八个 n、若干 k² 与 k²±1。
var checkN = append([]int64{
	0, 1, 15, 16, 17,
	4503599761588224, 4503599627370496, math.MaxInt64,
}, func() []int64 {
	ns := make([]int64, 0, 64)
	for _, k := range []int64{2, 3, 10, 100, 1000, 10000, 67108864, 67108865, 3037000499} {
		ns = append(ns, k*k-1, k*k, k*k+1)
	}
	return ns
}()...)

// SelfCheck 对内置 n 核验第二节的四条不变量，全部通过返回 nil。
func (a *API) SelfCheck() error {
	var errs []error

	// 不变量 1（定义精确）与 3（边界无 off-by-one）：
	// r² <= n < (r+1)²；上界用 n/(r+1) < r+1 表示，避免 (r+1)² 在 r=3037000499 时溢出。
	for _, n := range checkN {
		r, err := perfect.Isqrt(n)
		if err != nil {
			errs = append(errs, fmt.Errorf("selfcheck: isqrt(%d) error: %w", n, err))
			continue
		}
		if r*r > n || n/(r+1) >= r+1 {
			errs = append(errs, fmt.Errorf("selfcheck: invariant violated at n=%d r=%d", n, r))
		}
		if s, err := perfect.IsSquare(n); err != nil || s != (r*r == n) {
			errs = append(errs, fmt.Errorf("selfcheck: IsSquare mismatch at n=%d", n))
		}
	}

	// 不变量 2：与「从 0 逐个平方扫到超过 n」的朴素参照一致（分档到 10^6）。
	for b := int64(0); b <= 1_000_000; b += 101 {
		var k int64
		for (k+1)*(k+1) <= b {
			k++
		}
		if r, err := perfect.Isqrt(b); err != nil || r != k {
			errs = append(errs, fmt.Errorf("selfcheck: naive mismatch at n=%d", b))
			break
		}
	}

	// 不变量 4：三类失败互不相同、不 panic、不留半成品。
	if _, e := perfect.Isqrt(-1); !errors.Is(e, perfect.ErrNegative) {
		errs = append(errs, errors.New("selfcheck: negative error missing"))
	}
	if _, e := perfect.Isqrt(math.MinInt64); !errors.Is(e, perfect.ErrMinInt) {
		errs = append(errs, errors.New("selfcheck: minint error missing"))
	}
	if perfect.ErrNegative == perfect.ErrMinInt ||
		perfect.ErrNegative == perfect.ErrNotConverged ||
		perfect.ErrMinInt == perfect.ErrNotConverged {
		errs = append(errs, errors.New("selfcheck: sentinel errors not distinct"))
	}
	return errors.Join(errs...)
}
