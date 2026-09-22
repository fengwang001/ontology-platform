package coalesce

// less 比较两个区间的起点（起点相同再比终点），每调用一次记一次比较。
// 计数只统计区间之间的真实比较；线性合并不调用本函数，因此是 O(n) 且不计入。
func (n *Normalizer) less(a, b Interval) bool {
	n.compareCount++
	if a.Start != b.Start {
		return a.Start < b.Start
	}
	return a.End < b.End
}

// mergeSort 是自底向上的归并排序。每一层合并至多产生 n-1 次区间比较，
// 共 ceil(log2(n)) 层，故比较次数上界为 (n-1)*ceil(log2(n)) < n*ceil(log2(n))，
// 复杂度 O(n log n)，不存在两两比较的 O(n^2) 路径。
func (n *Normalizer) mergeSort(a []Interval) {
	if len(a) < 2 {
		return
	}
	buf := make([]Interval, len(a))
	for width := 1; width < len(a); width *= 2 {
		for left := 0; left < len(a); left += 2 * width {
			mid := left + width
			if mid > len(a) {
				mid = len(a)
			}
			right := left + 2*width
			if right > len(a) {
				right = len(a)
			}
			n.merge(a[left:mid], a[mid:right], buf)
		}
	}
}

// merge 合并两个各自有序的段，并把结果写回原切片对应位置。
func (n *Normalizer) merge(left, right, buf []Interval) {
	i, j, k := 0, 0, 0
	for i < len(left) && j < len(right) {
		if n.less(right[j], left[i]) {
			buf[k] = right[j]
			j++
		} else {
			buf[k] = left[i]
			i++
		}
		k++
	}
	for i < len(left) {
		buf[k] = left[i]
		i++
		k++
	}
	for j < len(right) {
		buf[k] = right[j]
		j++
		k++
	}
	copy(left[:len(left)], buf[:len(left)])
	copy(right[:len(right)], buf[len(left):k])
}
