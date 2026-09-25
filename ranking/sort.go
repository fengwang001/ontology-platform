package ranking

// defaultCompare 是默认的数值比较：+0.0 与 -0.0 相等，±Inf 参与两端的
// 正常排序。调用方保证不会传入 NaN。
func defaultCompare(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// less 是分区内排序用的完整比较器：先按排序值比较（降序时取反，
// 而非整体反转结果），值相等时一律按行 ID 升序兜底，保证并列内部
// 次序确定且与输入顺序无关。
func less(a, b Row, compare func(x, y float64) int, descending bool) bool {
	c := compare(a.SortValue, b.SortValue)
	if descending {
		c = -c
	}
	if c != 0 {
		return c < 0
	}
	return a.ID < b.ID
}
