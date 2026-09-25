// Package bucket 提供等宽直方图的桶号计算与桶区间表示。
// 桶 k 覆盖半开区间 [anchor+k*width, anchor+(k+1)*width)，左闭右开。
package bucket

// Interval 是半开区间 [Lo, Hi)。
type Interval struct {
	Lo, Hi int
}

// Contains 判定 v 是否落入半开区间 [Lo, Hi)。
func (i Interval) Contains(v int) bool { return v >= i.Lo && v < i.Hi }

// Index 返回 v 落入的桶号 k = floor((v-anchor)/width)，数学向下取整。
// 调用方保证 width > 0。
func Index(width, anchor, v int) int {
	return floorDiv(v-anchor, width)
}

// Of 返回桶 k 覆盖的半开区间。
func Of(width, anchor, k int) Interval {
	return Interval{Lo: anchor + k*width, Hi: anchor + (k+1)*width}
}

// floorDiv 数学向下取整除法：负值向负无穷取整，而非 Go 内建除法的向零截断。
func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}
