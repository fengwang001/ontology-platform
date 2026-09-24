// Package vec 提供固定维度 float64 向量、距离与内积以及维度校验。
package vec

import (
	"errors"
	"math"
)

// Vec 是固定维度的 float64 向量。
type Vec []float64

// ErrDimMismatch 表示向量/查询维度与索引维度不一致。
var ErrDimMismatch = errors.New("vec: dimension mismatch")

// ErrInvalidValue 表示向量含 NaN 或 Inf，必须拒绝。
var ErrInvalidValue = errors.New("vec: NaN or Inf value")

// Dim 返回向量维度。
func (v Vec) Dim() int { return len(v) }

// CheckDim 校验维度是否等于 want，否则返回包装了 ErrDimMismatch 的错误。
func (v Vec) CheckDim(want int) error {
	if len(v) != want {
		return dimError(want, len(v))
	}
	return nil
}

// Validate 校验维度并拒绝 NaN、±Inf。
func (v Vec) Validate(want int) error {
	if err := v.CheckDim(want); err != nil {
		return err
	}
	for _, x := range v {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return ErrInvalidValue
		}
	}
	return nil
}

// HasBadValue 报告是否含 NaN 或 ±Inf（用于跳过计数）。
func HasBadValue(v Vec) bool {
	for _, x := range v {
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return true
		}
	}
	return false
}

// Dot 返回内积；调用方需保证维度一致。
func Dot(a, b Vec) float64 {
	s := 0.0
	for i := range a {
		s += a[i] * b[i]
}
	return s
}

// SqDist 返回平方欧氏距离；调用方需保证维度一致。
func SqDist(a, b Vec) float64 {
	s := 0.0
	for i := range a {
		d := a[i] - b[i]
		s += d * d
	}
	return s
}

// Dist 返回欧氏距离。
func Dist(a, b Vec) float64 { return math.Sqrt(SqDist(a, b)) }

type dimErr struct {
	want, got int
}

func (e dimErr) Error() string {
	return "vec: dimension mismatch: expected " + itoa(e.want) + ", got " + itoa(e.got)
}

func (e dimErr) Is(target error) bool { return target == ErrDimMismatch }

func dimError(want, got int) error { return dimErr{want: want, got: got} }

// WantDim / GotDim 让调用方可从维度错误中取出期望与实际维度。
func WantDim(err error) int {
	var e dimErr
	if errors.As(err, &e) {
		return e.want
	}
	return -1
}

func GotDim(err error) int {
	var e dimErr
	if errors.As(err, &e) {
		return e.got
	}
	return -1
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
