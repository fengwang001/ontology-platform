package rot

func Search(nums []int, target int) int {
	index, _ := SearchCount(nums, target)
	return index
}

func SearchCount(nums []int, target int) (index, comparisons int) {
	lo, hi := 0, len(nums)-1
	for lo <= hi {
		mid := lo + (hi-lo)/2
		comparisons++
		if nums[mid] == target {
			return mid, comparisons
		}

		if nums[lo] <= nums[mid] {
			comparisons++
			if nums[lo] <= target && target < nums[mid] {
				hi = mid - 1
			} else {
				lo = mid + 1
			}
		} else {
			comparisons++
			if nums[mid] < target && target <= nums[hi] {
				lo = mid + 1
			} else {
				hi = mid - 1
			}
		}
	}
	return -1, comparisons
}
