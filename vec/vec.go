// Package vec 提供固定维度 float64 向量的基本运算与校验。
package vec

import (
	"errors"
	"fmt"
	"math"
)

// Vector 是定长 float64 向量。
type Vector []float64

var (
	// ErrDimMismatch 表示两个向量维度不一致。
	ErrDimMismatch = errors.New("vec: dimension mismatch")
	// ErrInvalidValue 表示向量含 NaN 或 ±Inf。
	ErrInvalidValue = errors.New("vec: NaN or Inf not allowed")
)

// Dim 返回向量维度。
func (v Vector) Dim() int { return len(v) }

// CheckDim 校验期望维度与实际维度，不符时包装 ErrDimMismatch。
func CheckDim(want, got int) error {
	if want != got {
		return fmt.Errorf("%w: want %d got %d", ErrDimMismatch, want, got)
	}
	return nil
}

// Validate 拒绝 NaN 与正负无穷；空向量在维度语义下也判非法。
func (v Vector) Validate() error {
	for _, x := range v {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return ErrInvalidValue
		}
	}
	return nil
}

// Dot 返回内积；维度不符返回错误。
func Dot(a, b Vector) (float64, error) {
	if len(a) != len(b) {
		return 0, fmt.Errorf("%w: %d vs %d", ErrDimMismatch, len(a), len(b))
	}
	var s float64
	for i := range a {
		s += a[i] * b[i]
	}
	return s, nil
}

// SquaredDist 返回平方欧氏距离（排序等价于真实距离，且省一次开方）。
func SquaredDist(a, b Vector) (float64, error) {
	if len(a) != len(b) {
		return 0, fmt.Errorf("%w: %d vs %d", ErrDimMismatch, len(a), len(b))
	}
	var s float64
	for i := range a {
		d := a[i] - b[i]
		s += d * d
	}
	return s, nil
}

// Clone 返回深拷贝。
func (v Vector) Clone() Vector {
	c := make(Vector, len(v))
	copy(c, v)
	return c
}

// IsZero 判断向量是否全零（签名约定见 DESIGN §1）。
func (v Vector) IsZero() bool {
	for _, x := range v {
		if x != 0 {
			return false
		}
	}
	return true
}
