package arr

import (
	"errors"

	"ontology/rot"
)

var (
	ErrNilInput      = errors.New("input slice is nil")
	ErrDuplicate     = errors.New("rotated array must be strictly increasing")
	ErrInvalidRotate = errors.New("input is not a single rotated sorted array")
)

func Search(nums []int, target int) (int, error) {
	if err := Validate(nums); err != nil {
		return -1, err
	}
	return rot.Search(nums, target), nil
}

func Validate(nums []int) error {
	if nums == nil {
		return ErrNilInput
	}
	if len(nums) < 2 {
		return nil
	}

	wraps := 0
	for i := 1; i < len(nums); i++ {
		if nums[i] == nums[i-1] {
			return ErrDuplicate
		}
		if nums[i] < nums[i-1] {
			wraps++
		}
	}
	if wraps > 1 {
		return ErrInvalidRotate
	}
	return nil
}
