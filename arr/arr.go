// Package arr 在 rot 之上提供输入校验与哨兵错误。
package arr

import (
	"errors"

	"ontology/rot"
)

var (
	// ErrEmpty 表示空切片（调用方按约定可忽略，直接按未命中处理）。
	ErrEmpty = errors.New("arr: empty slice")
	// ErrDuplicate 表示出现重复元素，不满足严格升序旋转前提。
	ErrDuplicate = errors.New("arr: duplicate elements")
	// ErrNotRotated 表示无重复但不是一次循环右移的结果（断点超过一个）。
	ErrNotRotated = errors.New("arr: not a cyclic shift of a sorted slice")
)

// Validate 校验 nums 是否为严格升序数组经一次循环右移的结果。
// 空切片返回 ErrEmpty；重复元素返回 ErrDuplicate；断点过多返回 ErrNotRotated。
func Validate(nums []int) error {
	if len(nums) == 0 {
		return ErrEmpty
	}
	if len(nums) == 1 {
		return nil
	}
	drops := 0
	for i := 0; i < len(nums); i++ {
		v := nums[i]
		next := nums[(i+1)%len(nums)]
		switch {
		case v == next:
			return ErrDuplicate
		case v > next:
			drops++
		}
	}
	if drops > 1 {
		return ErrNotRotated
	}
	return nil
}

// Search 先校验再查找。空切片返回 (-1, ErrEmpty)；其余非法输入
// 返回 (-1, 对应哨兵错误)；合法时行为与 rot.Search 一致。
func Search(nums []int, target int) (int, error) {
	if err := Validate(nums); err != nil {
		return -1, err
	}
	return rot.Search(nums, target), nil
}
