// Package bucket 提供等宽直方图的桶号计算与桶区间表示。
// 桶 k 覆盖半开区间 [anchor+k*width, anchor+(k+1)*width)，左闭右开。
package bucket

// Number 返回值 v 落入的桶号 k = floor((v - anchor) / width)。
// 使用数学向下取整：负值向负无穷取整，而不是 Go 整数除法的向零截断。
// 前置条件：width > 0（由上层 New 保证）。
func Number(v, anchor, width int) int {
	d := v - anchor
	q := d / width
	if r := d % width; r < 0 { // 余数为负说明截断除法向零多算了 1
		q--
	}
	return q
}

// Lo 返回桶 k 覆盖区间的左端点（含）。
func Lo(anchor, width, k int) int { return anchor + k*width }

// Hi 返回桶 k 覆盖区间的右端点（不含）。
func Hi(anchor, width, k int) int { return anchor + (k+1)*width }

// Contains 判定 v 是否落入桶 k：等价于 Number(v) == k，
// 即 Lo(k) <= v < Hi(k)，右边界值归下一个桶。
func Contains(v, anchor, width, k int) bool {
	return Number(v, anchor, width) == k
}
