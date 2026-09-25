package rot

func Search(nums []int, target int) int {
	index, _ := searchWithCount(nums, target)
	return index
}

func SearchCount(nums []int, target int) (int, int) {
	return searchWithCount(nums, target)
}

func searchWithCount(nums []int, target int) (int, int) {
	if len(nums) == 0 {
		return -1, 0
	}

	lo, hi := 0, len(nums)-1
	comparisons := 0
	less := func(left, right int) bool {
		comparisons++
		return left < right
	}

	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if less(nums[hi], nums[mid]) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	pivot := lo

	if less(nums[len(nums)-1], target) {
		lo, hi = 0, pivot
	} else {
		lo, hi = pivot, len(nums)-1
	}
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if less(nums[mid], target) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}

	comparisons++
	if nums[lo] == target {
		return lo, comparisons
	}
	return -1, comparisons
}
