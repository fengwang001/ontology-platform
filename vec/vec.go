// Package vec 定义固定维度的 float64 向量及基本运算。
package vec

import (
	"errors"
	"math"
)

// Vector 是带 ID 的向量。
type Vector struct {
	ID uint64
	V  []float64
}

var (
	// ErrEmpty 表示空向量。
	ErrEmpty = errors.New("vec: empty vector")
	// ErrDim 表示两个向量维度不一致。
	ErrDim = errors.New("vec: dimension mismatch")
	// ErrBadValue 表示向量含 NaN 或 Inf。
	ErrBadValue = errors.New("vec: NaN or Inf value")
)

// CheckDim 返回 a、b 长度一致且非空时的维度，否则返回可判定错误。
func CheckDim(a, b []float64) (int, error) {
	if len(a) == 0 || len(b) == 0 {
		return 0, ErrEmpty
	}
	if len(a) != len(b) {
		return 0, ErrDim
	}
	return len(a), nil
}

// Validate 拒绝含 NaN / ±Inf 的向量。
func Validate(v []float64) error {
	if len(v) == 0 {
		return ErrEmpty
	}
	for _, x := range v {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return ErrBadValue
		}
	}
	return nil
}

// Dot 返回内积；维度不一致时返回 ErrDim。
func Dot(a, b []float64) (float64, error) {
	if _, err := CheckDim(a, b); err != nil {
		return 0, err
	}
	var s float64
	for i := range a {
		s += a[i] * b[i]
	}
	return s, nil
}

// Dist 返回欧氏距离；维度不一致时返回 ErrDim。
func Dist(a, b []float64) (float64, error) {
	if _, err := CheckDim(a, b); err != nil {
		return 0, err
	}
	var s float64
	for i := range a {
		d := a[i] - b[i]
		s += d * d
	}
	return math.Sqrt(s), nil
}
