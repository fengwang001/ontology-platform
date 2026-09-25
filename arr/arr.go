package arr

import (
	"errors"

	"ontology/rot"
)

var (
	ErrEmpty      = errors.New("empty array")
	ErrDuplicate  = errors.New("duplicate value")
	ErrNotRotated = errors.New("array is not a strict cyclic rotation")
)

func Validate(nums []int) error {
	if len(nums) == 0 {
		return ErrEmpty
	}

	breakpoints := 0
	for i := 1; i < len(nums); i++ {
		if nums[i-1] == nums[i] {
			return ErrDuplicate
		}
		if nums[i-1] > nums[i] {
			breakpoints++
		}
	}
	if breakpoints > 1 || breakpoints == 1 && nums[0] < nums[len(nums)-1] {
		return ErrNotRotated
	}
	return nil
}

func Search(nums []int, target int) int {
	if len(nums) == 0 || Validate(nums) != nil {
		return -1
	}
	return rot.Search(nums, target)
}
