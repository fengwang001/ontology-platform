// Package vec 提供固定维度 float64 向量的基本运算与校验。
package vec

import (
	"errors"
	"math"
)

// Vec 是定长 float64 向量。
type Vec []float64

// DimError 表示实际维度与期望维度不符，可通过 errors.Is(err, ErrDim) 判定。
type DimError struct {
	Want int
	Got  int
}

func (e DimError) Error() string {
	return "vec: dimension mismatch: want " + itoa(e.Want) + " got " + itoa(e.Got)
}

// ErrDim 用于 errors.Is 匹配任意维度不符。
var ErrDim = errors.New("dimension mismatch")

func (e DimError) Is(target error) bool { return target == ErrDim }

var (
	// ErrNaN 表示向量含 NaN。
	ErrNaN = errors.New("vec: contains NaN")
	// ErrInf 表示向量含 +Inf 或 -Inf。
	ErrInf = errors.New("vec: contains Inf")
)

// CheckDim 在维度不符时返回带期望/实际值的 DimError。
func CheckDim(got, want int) error {
	if got != want {
		return DimError{Want: want, Got: got}
	}
	return nil
}

// Validate 校验维度为正且不含 NaN/±Inf，顺序：维度、NaN、Inf。
func Validate(v Vec, dim int) error {
	if err := CheckDim(len(v), dim); err != nil {
		return err
	}
	for _, x := range v {
		if math.IsNaN(x) {
			return ErrNaN
		}
	}
	for _, x := range v {
		if math.IsInf(x, 0) {
			return ErrInf
		}
	}
	return nil
}

// Dot 返回内积；调用方须保证维度一致。
func Dot(a, b Vec) float64 {
	s := 0.0
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

// Distance 返回欧氏平方距离（精排只需相对顺序，省去开方）。
func Distance(a, b Vec) float64 {
	s := 0.0
	for i := range a {
		d := a[i] - b[i]
		s += d * d
	}
	return s
}

// Equal 逐分量严格相等。
func Equal(a, b Vec) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
