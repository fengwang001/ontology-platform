// Package api 是分块矩阵乘法的对外入口：构造引擎、执行乘法、自检。
//
// 本包只向下依赖 mul 与 blk（依赖方向单向；blk/mul 绝不反向依赖 api）。
package api

import (
	"errors"
	"math"

	"ontology/blk"
	"ontology/mul"
)

// 三类互不相同的哨兵错误：失败必须可用 errors.Is 判定。
var (
	// ErrEmptyMatrix：n < 1（空矩阵）。
	ErrEmptyMatrix = errors.New("api: matrix dimension n must be >= 1")
	// ErrBlockSize：块大小 b < 1 或 b > n。
	ErrBlockSize = errors.New("api: block size b must satisfy 1 <= b <= n")
	// ErrShapeMismatch：a 或 b 的长度不是 n*n。
	ErrShapeMismatch = errors.New("api: input length must be n*n")
)

// Engine 持有固定块大小；除构造时写入的 b 外无可变状态。
// 内部计数器位于 blk 包（非导出、原子保护），拒绝路径绝不触碰它。
type Engine struct {
	b int
}

// New 以块大小 b 构造引擎。b 对具体 n 是否合法在 Mul 时整体校验
// （此处尚不知道 n，无法判断 b > n）。
func New(b int) *Engine { return &Engine{b: b} }

// validate 在任何计算之前做整体前置校验，返回哨兵错误。
// 顺序固定：先空矩阵，再块大小，最后维度——任一不过立即整体失败，
// 不调用 mul.Blocked / blk.TailBlock，故不留任何状态痕迹。
func (e *Engine) validate(a, x []float64, n int) error {
	if n < 1 {
		return ErrEmptyMatrix
	}
	if e.b < 1 || e.b > n {
		return ErrBlockSize
	}
	if len(a) != n*n || len(x) != n*n {
		return ErrShapeMismatch
	}
	return nil
}

// Mul 在只读的 a、b 上计算分块乘法，返回全新切片，不修改输入。
// 多个 goroutine 可并发对同一份输入调用。
func (e *Engine) Mul(a, b []float64, n int) ([]float64, error) {
	if err := e.validate(a, b, n); err != nil {
		return nil, err
	}
	return mul.Blocked(a, b, n, e.b), nil
}

// bitsEqual 逐位比较两个等长 float64 切片。
func bitsEqual(x, y []float64) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if math.Float64bits(x[i]) != math.Float64bits(y[i]) {
			return false
		}
	}
	return true
}

// derived5 返回 NOTES 第三节的内置 5x5 矩阵。
func derived5() (a, b []float64) {
	const n = 5
	a, b = make([]float64, n*n), make([]float64, n*n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			a[i*n+j] = float64(i*n + j + 1)
			b[i*n+j] = float64(i - j) // 含负值
		}
	}
	return a, b
}

// SelfCheck 用内置矩阵核验四条不变量与 O(1) 尾块定位，全过返回 nil。
func (e *Engine) SelfCheck() error {
	a, b := derived5()
	// 不变量 1：分块与朴素逐位相等（多块大小，覆盖尾块）。
	ref := mul.Naive(a, b, 5)
	for _, bs := range []int{1, 2, 3, 5} {
		if !bitsEqual(mul.Blocked(a, b, 5, bs), ref) {
			return errors.New("api: self-check invariant 1 (blocked==naive) failed")
		}
	}
	if ref[3*5+2] != 10 {
		return errors.New("api: self-check derived C[3][2] != 10")
	}
	// 不变量 2：块覆盖完备。
	for _, c := range []struct{ n, b int }{{5, 2}, {17, 17}, {100, 17}} {
		ps := blk.Parts(c.n, c.b)
		if ps[0].Start != 0 || ps[len(ps)-1].End != c.n {
			return errors.New("api: self-check invariant 2 (coverage) failed")
		}
	}
	// 不变量 3：重复调用逐位稳定。
	first := mul.Blocked(a, b, 5, 2)
	for r := 0; r < 3; r++ {
		if !bitsEqual(mul.Blocked(a, b, 5, 2), first) {
			return errors.New("api: self-check invariant 3 (determinism) failed")
		}
	}
	// 不变量 4：三类拒绝互不相同且不留痕——拒绝后引擎仍给出基线结果。
	good := New(2)
	sentinels := make([]error, 5)
	_, sentinels[0] = good.Mul(a, b, 0)      // 空矩阵
	_, sentinels[1] = New(0).Mul(a, b, 5)    // 块过小
	_, sentinels[2] = New(6).Mul(a, b, 5)    // 块过大
	_, sentinels[3] = good.Mul(a[:24], b, 5) // a 维度不符
	_, sentinels[4] = good.Mul(a, b[:24], 5) // b 维度不符
	want := []error{ErrEmptyMatrix, ErrBlockSize, ErrBlockSize, ErrShapeMismatch, ErrShapeMismatch}
	for i := range want {
		if !errors.Is(sentinels[i], want[i]) {
			return errors.New("api: self-check invariant 4 (sentinel) failed")
		}
	}
	got, err := good.Mul(a, b, 5)
	if err != nil || !bitsEqual(got, ref) {
		return errors.New("api: self-check invariant 4 (state untouched) failed")
	}
	return blk.SelfCheck() // O(1) 闭式尾块定位
}
