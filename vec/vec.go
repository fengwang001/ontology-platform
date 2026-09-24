// Package vec 提供固定维度 float64 向量及基础运算与合法性校验。
package vec

import (
	"errors"
	"math"
)

// Vec 是固定维度的 float64 向量。
type Vec []float64

var (
	// ErrDimMismatch 表示参与运算的向量维度不一致；
	// 通过 errors.As 可取 *DimError 查看期望/实际维度。
	ErrDimMismatch = errors.New("vec: dimension mismatch")
	// ErrInvalidValue 表示向量含 NaN 或 ±Inf。
	ErrInvalidValue = errors.New("vec: NaN or Inf not allowed")
	// ErrEmpty 表示空向量（维度为 0）。
	ErrEmpty = errors.New("vec: empty vector")
)

// DimError 记录期望与实际维度，支持 errors.Is(ErrDimMismatch)。
type DimError struct {
	Want int
	Got  int
}

func (e *DimError) Error() string { return "vec: dimension mismatch" }
func (e *DimError) Is(target error) bool { return target == ErrDimMismatch }

// CheckDim 校验 v 维度是否等于 d，否则返回包装 *DimError 的错误。
func CheckDim(v Vec, d int) error {
	if len(v) != d {
		return &DimError{Want: d, Got: len(v)}
	}
	return nil
}

// Validate 拒绝空向量及含 NaN/±Inf 的向量。
func Validate(v Vec) error {
	if len(v) == 0 {
		return ErrEmpty
	}
	for _, x := range v {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return ErrInvalidValue
		}
	}
	return nil
}

// Dot 返回内积；维度不一致返回错误。
func Dot(a, b Vec) (float64, error) {
	if len(a) != len(b) {
		return 0, &DimError{Want: len(a), Got: len(b)}
	}
	var s float64
	for i := range a {
		s += a[i] * b[i]
	}
	return s, nil
}

// SquaredDist 返回欧氏距离平方；维度不一致返回错误。
func SquaredDist(a, b Vec) (float64, error) {
	if len(a) != len(b) {
		return 0, &DimError{Want: len(a), Got: len(b)}
	}
	var s float64
	for i := range a {
		d := a[i] - b[i]
		s += d * d
	}
	return s, nil
}
