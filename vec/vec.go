// Package vec 定义固定维度 float64 向量及其基本运算与校验。
package vec

import (
	"errors"
	"fmt"
	"math"
)

// Vector 是固定维度的 float64 向量。
type Vector []float64

// ErrDimMismatch 表示两个向量维度不一致。
var ErrDimMismatch = errors.New("vec: dimension mismatch")

// ErrInvalidValue 表示向量含 NaN 或 Inf。
var ErrInvalidValue = errors.New("vec: NaN or Inf not allowed")

// Dim 返回向量维度。
func Dim(v Vector) int { return len(v) }

// CheckDim 校验期望维度与实际维度，不符返回包装了 ErrDimMismatch 的错误。
func CheckDim(want, got int) error {
	if want != got {
		return fmt.Errorf("vec: dimension mismatch: want=%d got=%d: %w",
			want, got, ErrDimMismatch)
	}
	return nil
}

// Dot 返回两向量内积；维度不符返回 ErrDimMismatch。
func Dot(a, b Vector) (float64, error) {
	if len(a) != len(b) {
		return 0, ErrDimMismatch
	}
	var sum float64
	for i := range a {
		sum += a[i] * b[i]
	}
	return sum, nil
}

// Euclidean 返回两向量欧氏距离；维度不符返回 ErrDimMismatch。
func Euclidean(a, b Vector) (float64, error) {
	if len(a) != len(b) {
		return 0, ErrDimMismatch
	}
	var sum float64
	for i := range a {
		d := a[i] - b[i]
		sum += d * d
	}
	return math.Sqrt(sum), nil
}

// EuclideanSq 返回平方欧氏距离，精排时避免开方开销。
func EuclideanSq(a, b Vector) (float64, error) {
	if len(a) != len(b) {
		return 0, ErrDimMismatch
	}
	var sum float64
	for i := range a {
		d := a[i] - b[i]
		sum += d * d
	}
	return sum, nil
}

// Validate 拒绝含 NaN 或 ±Inf 的向量。
func Validate(v Vector) error {
	for _, x := range v {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return ErrInvalidValue
		}
	}
	return nil
}

// HasNaN 报告向量是否含 NaN（用于构造跳过分类）。
func HasNaN(v Vector) bool {
	for _, x := range v {
		if math.IsNaN(x) {
			return true
		}
	}
	return false
}

// HasInf 报告向量是否含 ±Inf。
func HasInf(v Vector) bool {
	for _, x := range v {
		if math.IsInf(x, 0) {
			return true
		}
	}
	return false
}
