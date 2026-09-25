// Package cnt 用归并排序变体在 O(n log n) 时间内统计逆序对数量。
package cnt

// CountInversions 返回 arr 中满足 i < j 且 arr[i] > arr[j] 的下标对数。
// 空数组与单元素数组返回 0。函数是纯函数：不修改入参、不持有进程状态。
func CountInversions(arr []int) (int64, error) {
	inv, _, err := CountInversionsWithStats(arr)
	return inv, err
}

// CountInversionsWithStats 在计数之外额外返回归并过程中的元素比较次数。
// 该计数器是函数内部的非导出量，每次调用独立，因此并发调用互不影响。
func CountInversionsWithStats(arr []int) (inversions int64, comparisons int64, err error) {
	n := len(arr)
	if n < 2 {
		return 0, 0, nil
	}
	work := make([]int, n)
	copy(work, arr)
	aux := make([]int, n)
	inversions = sortAndCount(work, aux, 0, n-1, &comparisons)
	return inversions, comparisons, nil
}

// sortAndCount 原地排序 work[lo:hi+1]，返回其中的逆序对数，
// 并把每次元素比较累加到 comparisons。
func sortAndCount(work, aux []int, lo, hi int, comparisons *int64) int64 {
	if lo >= hi {
		return 0
	}
	mid := int(uint(lo+hi) >> 1)
	var inversions int64
	inversions += sortAndCount(work, aux, lo, mid, comparisons)
	inversions += sortAndCount(work, aux, mid+1, hi, comparisons)

	i, j, k := lo, mid+1, lo
	for i <= mid && j <= hi {
		*comparisons++
		// 相等时先取左半，保证归并稳定且不把相等元素计为逆序对。
		if work[i] <= work[j] {
			aux[k] = work[i]
			i++
		} else {
			// work[i..mid] 都严格大于 work[j]，全部与 work[j] 成逆序对。
			inversions += int64(mid - i + 1)
			aux[k] = work[j]
			j++
		}
		k++
	}
	for i <= mid {
		aux[k] = work[i]
		i++
		k++
	}
	for j <= hi {
		aux[k] = work[j]
		j++
		k++
	}
	copy(work[lo:hi+1], aux[lo:hi+1])
	return inversions
}
