package rot

func Search(nums []int, target int) int {
	index, _ := SearchWithComparisons(nums, target)
	return index
}

func SearchWithComparisons(nums []int, target int) (index, comparisons int) {
	if len(nums) == 0 {
		return -1, 0
	}

	lo, hi := 0, len(nums)-1
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		comparisons++
		if nums[mid] <= nums[len(nums)-1] {
			hi = mid
		} else {
			lo = mid + 1
		}
	}

	pivot := lo
	size := len(nums)
	left, right := 0, size
	for left < right {
		mid := int(uint(left+right) >> 1)
		value := nums[(pivot+mid)%size]
		comparisons++
		if value < target {
			left = mid + 1
		} else {
			right = mid
		}
	}
	index = (pivot + left) % size
	comparisons++
	if left < size && nums[index] == target {
		return index, comparisons
	}
	return -1, comparisons
}
