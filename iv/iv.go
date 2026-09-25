// Package iv 定义左闭右开区间 [Start, End) 与基础校验。
package iv

import (
	"errors"
	"fmt"
)

// 三类校验错误，均可用 errors.Is 区分；
// ErrZeroLength 与 ErrReversed 同时匹配 ErrEmptyInterval。
var (
	ErrEmptyInterval = errors.New("iv: empty interval")
	ErrZeroLength    = fmt.Errorf("iv: start == end: %w", ErrEmptyInterval)
	ErrReversed      = fmt.Errorf("iv: start > end: %w", ErrEmptyInterval)
)

// Interval 表示整数值域 [Start, End)：Start <= x < End。
type Interval struct {
	Start int
	End   int
}

// New 构造区间并校验：start >= end 时报错。
func New(start, end int) (Interval, error) {
	if start > end {
		return Interval{}, ErrReversed
	}
	if start == end {
		return Interval{}, ErrZeroLength
	}
	return Interval{Start: start, End: end}, nil
}

// Must 构造区间，非法时 panic；主要用于测试与字面量。
func Must(start, end int) Interval {
	v, err := New(start, end)
	if err != nil {
		panic(err)
	}
	return v
}
