// Package vec 提供固定维度 float64 向量的基本定义与距离运算。
package vec

import (
	"errors"
	"fmt"
	"math"
)

// Vector 是定长 float64 向量；长度即维度。
type Vector []float64

var (
	// ErrDimMismatch 表示向量维度与索引期望维度不符。
	ErrDimMismatch = errors.New("vec: dimension mismatch")
	// ErrInvalidValue 表示向量含 NaN 或 ±Inf。
	ErrInvalidValue = errors.New("vec: invalid float value (NaN or Inf)")
)

// CheckDim 返回带期望/实际维度信息的维度错误。
func CheckDim(got, want int) error {
	if got != want {
		return fmt.Errorf("%w: expected dimension %d, got %d", ErrDimMismatch, want, got)
	}
	return nil
}

// Validate 校验维度与取值合法性：维度不符或含 NaN/±Inf 均返回错误。
func Validate(v Vector, want int) error {
	if err := CheckDim(len(v), want); err != nil {
		return err
	}
	for _, x := range v {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return ErrInvalidValue
		}
	}
	return nil
}

// Dot 返回内积；调用方需保证两向量等长（先经 Validate）。
func Dot(a, b Vector) float64 {
	var s float64
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

// Euclidean 返回欧氏距离；调用方需保证两向量等长（先经 Validate）。
func Euclidean(a, b Vector) float64 {
	var s float64
	for i := range a {
		d := a[i] - b[i]
		s += d * d
	}
	return math.Sqrt(s)
}
