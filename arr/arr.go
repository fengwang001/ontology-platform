// Package arr 提供输入校验与哨兵错误，并委托 cnt 完成计数。
package arr

import (
	"errors"

	"ontology/cnt"
)

// MaxN 是溢出安全边界：n <= MaxN 时计数不超过 n(n-1)/2，int64 不溢出。
const MaxN = 100000

// 三类哨兵错误，可用 errors.Is 区分。
var (
	ErrNilSlice = errors.New("arr: 输入为 nil 切片")
	ErrOversize = errors.New("arr: 输入长度超过 MaxN")
	ErrNegative = errors.New("arr: 存在负数元素")
)

// Validate 校验输入，返回对应哨兵错误；合法时返回 nil。
func Validate(a []int) error {
	if a == nil {
		return ErrNilSlice
	}
	if len(a) > MaxN {
		return ErrOversize
	}
	for _, v := range a {
		if v < 0 {
			return ErrNegative
		}
	}
	return nil
}

// Count 先校验再计数；校验失败时返回 0 与哨兵错误。
func Count(a []int) (int64, error) {
	if err := Validate(a); err != nil {
		return 0, err
	}
	return cnt.CountInversions(a)
}
