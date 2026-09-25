// Package arr 校验输入是否为「严格递增序列的循环右移」，再委托 rot 查找。
// 三类非法输入用哨兵错误区分，可用 errors.Is 判定。
package arr

import (
	"errors"

	"ontology/rot"
)

var (
	// ErrEmpty 输入为 nil 或空切片。
	ErrEmpty = errors.New("arr: empty slice")
	// ErrDuplicate 存在相邻相等元素（非严格递增，违反旋转排序定义）。
	ErrDuplicate = errors.New("arr: duplicate values")
	// ErrNotRotation 不是合法旋转：下降点超过一个，或首尾环绕关系错误。
	ErrNotRotation = errors.New("arr: not a rotation of a sorted array")
)

// Validate 校验 nums 是否为严格递增序列的循环右移（含未旋转的有序序列）。
func Validate(nums []int) error {
	if len(nums) == 0 {
		return ErrEmpty
	}
	drops := 0
	for i := 1; i < len(nums); i++ {
		switch {
		case nums[i] == nums[i-1]:
			return ErrDuplicate
		case nums[i] < nums[i-1]:
			drops++
		}
	}
	switch {
	case drops > 1:
		return ErrNotRotation
	case drops == 1 && nums[0] < nums[len(nums)-1]:
		// 形如 [2,1,3]：有一个下降点但不是任何有序序列的旋转。
		return ErrNotRotation
	default:
		return nil
	}
}

// Search 先校验再查找。空切片按约定返回 (-1, nil)；非法输入返回
// (-1, 哨兵错误)；合法输入返回 rot.Search 的结果。
func Search(nums []int, target int) (int, error) {
	if len(nums) == 0 {
		return -1, nil
	}
	if err := Validate(nums); err != nil {
		return -1, err
	}
	return rot.Search(nums, target), nil
}
