package arr

import (
	"errors"

	"ontology/rot"
)

var (
	ErrEmpty            = errors.New("arr: empty array")
	ErrDuplicate        = errors.New("arr: duplicate element")
	ErrNotRotatedSorted = errors.New("arr: not a rotated sorted array")
)

func Search(nums []int, target int) (int, error) {
	if len(nums) == 0 {
		return -1, nil
	}
	if err := Validate(nums); err != nil {
		return -1, err
	}
	return rot.Search(nums, target), nil
}

func Validate(nums []int) error {
	if len(nums) == 0 {
		return ErrEmpty
	}
	rotations := 0
	for index := 1; index < len(nums); index++ {
		switch {
		case nums[index] == nums[index-1]:
			return ErrDuplicate
		case nums[index] < nums[index-1]:
			rotations++
		}
	}
	if rotations > 1 {
		return ErrNotRotatedSorted
	}
	return nil
}
