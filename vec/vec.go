// Package vec 定义定维 float64 向量、欧氏距离、内积与维度/合法性校验。
package vec

import (
	"errors"
	"fmt"
	"math"
)

// Vec 是一个定维 float64 向量。
type Vec []float64

var (
	// ErrDim 表示维度不符，错误信息中带期望与实际维度。
	ErrDim = errors.New("vec: dimension mismatch")
	// ErrNaN 表示向量含 NaN 分量。
	ErrNaN = errors.New("vec: NaN component")
	// ErrInf 表示向量含 ±Inf 分量。
	ErrInf = errors.New("vec: infinite component")
)

// Check 校验 v 的维度等于 dim 且不含 NaN/±Inf；合法返回 nil。
func Check(v Vec, dim int) error {
	if len(v) != dim {
		return fmt.Errorf("%w: want dim %d, got %d", ErrDim, dim, len(v))
	}
	for i, x := range v {
		if math.IsNaN(x) {
			return fmt.Errorf("%w at index %d", ErrNaN, i)
		}
		if math.IsInf(x, 0) {
			return fmt.Errorf("%w at index %d", ErrInf, i)
		}
	}
	return nil
}

// Dot 返回 a·b。调用方需保证 len(a)==len(b)。
func Dot(a, b Vec) float64 {
	var s float64
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

// Dist 返回 a、b 的欧氏距离。调用方需保证 len(a)==len(b)。
func Dist(a, b Vec) float64 {
	var s float64
	for i := range a {
		d := a[i] - b[i]
		s += d * d
	}
	return math.Sqrt(s)
}
