// Package arr 负责旋转排序数组的输入校验，并委托 rot 完成查找。
package arr

import (
	"errors"

	"ontology/rot"
)

var (
	// ErrNilInput 表示传入的切片为 nil。
	ErrNilInput = errors.New("arr: input slice is nil")
	// ErrDuplicate 表示数组非严格（出现相等元素）。
	ErrDuplicate = errors.New("arr: array is not strictly increasing")
	// ErrMalformed 表示数组不是有序数组的一次循环右移（跌落点超过一个）。
	ErrMalformed = errors.New("arr: array is not a rotated sorted array")
)

// Validate 检查 nums 是否为严格递增数组经一次循环右移的结果。
// nil 返回 ErrNilInput；空切片视为合法；相等元素返回 ErrDuplicate；
// 相邻跌落（a[i]>a[i+1]）超过一次返回 ErrMalformed。
func Validate(nums []int) error {
	if nums == nil {
		return ErrNilInput
	}
	descents := 0
	for i := 1; i < len(nums); i++ {
		switch {
		case nums[i-1] == nums[i]:
			return ErrDuplicate
		case nums[i-1] > nums[i]:
			descents++
			if descents > 1 {
				return ErrMalformed
			}
		}
	}
	return nil
}

// Search 先校验再查找。空数组返回 (-1,nil)；输入不合法时返回对应哨兵错误。
func Search(nums []int, target int) (int, error) {
	if err := Validate(nums); err != nil {
		return -1, err
	}
	return rot.Search(nums, target), nil
}
