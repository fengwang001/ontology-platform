package cell

// Cell 表示一个 CSV 字段：值、是否加过引号、原文起止字节偏移（半开区间）。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 比较两字段逐字段相等且引号标记相同。
func (a Cell) Equal(b Cell) bool {
	return a.Quoted == b.Quoted && a.Value == b.Value && a.Start == b.Start && a.End == b.End
}
