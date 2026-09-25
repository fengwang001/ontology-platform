// Package arr 校验严格旋转排序数组并包装 rot.Search。
package arr

import (
	"errors"

	"ontology/rot"
)

// 三类输入非法的哨兵错误，可用 errors.Is 区分。
var (
	ErrNotStrict   = errors.New("arr: adjacent elements are not strictly increasing")
	ErrMultiPivot  = errors.New("arr: more than one rotation point")
	ErrBadRotation = errors.New("arr: last element is not smaller than first after a drop")
)

// Validate 判定 nums 是否为空或合法的严格旋转排序数组。
// 合法形态：相邻元素严格递增，且至多有一处下降；若有一处下降，
// 则末元素必须小于首元素（即确为有序数组旋转而来）。
func Validate(nums []int) error {
	drops := 0
	for i := 1; i < len(nums); i++ {
		switch {
		case nums[i] == nums[i-1]:
			return ErrNotStrict
		case nums[i] < nums[i-1]:
			drops++
			if drops > 1 {
				return ErrMultiPivot
			}
		}
	}
	if drops == 1 && nums[len(nums)-1] >= nums[0] {
		return ErrBadRotation
	}
	return nil
}

// Search 先校验输入，再委托 rot.Search；空数组返回 -1 且无错误。
func Search(nums []int, target int) (int, error) {
	if err := Validate(nums); err != nil {
		return -1, err
	}
	return rot.Search(nums, target), nil
}
