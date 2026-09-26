// Package fn 封装被积函数 f 的表示与求值，不依赖其他包。
package fn

import "errors"

// ErrNilFunc 表示被积函数缺失（f 为 nil）。
var ErrNilFunc = errors.New("fn: nil integrand")

// Func 是被积函数 f: R -> R 的封装。
type Func struct {
	g func(float64) float64
}

// New 包装一个 Go 函数为被积函数；g 为 nil 时返回 ErrNilFunc。
func New(g func(float64) float64) (Func, error) {
	if g == nil {
		return Func{}, ErrNilFunc
	}
	return Func{g: g}, nil
}

// At 求值 f(x)。零值 Func（未持有函数）调用会 panic，构造侧已保证不会发生。
func (f Func) At(x float64) float64 {
	return f.g(x)
}
