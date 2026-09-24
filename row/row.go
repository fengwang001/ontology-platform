// Package row 定义数据集中的行及其复合排序键的全序关系。
package row

// Row 是数据集中的一行：一个 float64 排序值加一个唯一字符串 ID。
type Row struct {
	Key float64
	ID  string
}

// Compare 比较两行的复合键 (Key, ID)：先比 Key，相等再比 ID。
// 返回值小于、等于、大于 0 分别表示 a 小于、等于、大于 b。
// ID 唯一保证该序为全序。
func Compare(a, b Row) int {
	switch {
	case a.Key < b.Key:
		return -1
	case a.Key > b.Key:
		return 1
	case a.ID < b.ID:
		return -1
	case a.ID > b.ID:
		return 1
	default:
		return 0
	}
}

// Less 报告 a 的复合键是否严格小于 b。
func Less(a, b Row) bool { return Compare(a, b) < 0 }
