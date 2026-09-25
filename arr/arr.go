// Package arr 负责排序质量度量的输入校验，并委托 cnt 统计逆序对。
package arr

import (
	"errors"

	"ontology/cnt"
)

// MaxLen 是可接受的最大输入长度；n=100000 时逆序对数上界
// n(n-1)/2 约 5e9，仍在 int64 范围内。
const MaxLen = 100_000

// 三类哨兵错误，调用方可用 errors.Is 区分。
var (
	// ErrNilSlice 表示传入的切片为 nil（空语义与 nil 显式区分）。
	ErrNilSlice = errors.New("arr: input slice is nil")
	// ErrTooLong 表示输入长度超过 MaxLen。
	ErrTooLong = errors.New("arr: input slice exceeds maximum length")
	// ErrNegativeValue 表示元素为负数；本体实例标识不允许负值。
	ErrNegativeValue = errors.New("arr: input contains negative value")
)

// Validate 校验输入：非 nil、长度不超限、元素非负。
func Validate(input []int) error {
	if input == nil {
		return ErrNilSlice
	}
	if len(input) > MaxLen {
		return ErrTooLong
	}
	for _, v := range input {
		if v < 0 {
			return ErrNegativeValue
		}
	}
	return nil
}

// Count 先校验输入，再统计逆序对。校验失败时返回对应哨兵错误与计数 0。
func Count(input []int) (int64, error) {
	if err := Validate(input); err != nil {
		return 0, err
	}
	return cnt.CountInversions(input)
}
