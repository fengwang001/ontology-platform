// Package fn 封装被积函数 f 的构造与求值。不依赖其他包。
package fn

import "errors"

// ErrNilFunc 表示构造时传入的被积函数为 nil。
var ErrNilFunc = errors.New("fn: nil function")

// Func 是被积函数 f 的封装，保证内部 f 非 nil。
type Func struct {
	f func(float64) float64
}

// New 包装一个被积函数；f 为 nil 时返回 ErrNilFunc。
func New(f func(float64) float64) (*Func, error) {
	if f == nil {
		return nil, ErrNilFunc
	}
	return &Func{f: f}, nil
}

// Eval 计算 f(x)。
func (fn *Func) Eval(x float64) float64 { return fn.f(x) }
