package cell

// Cell 表示一个 CSV 字段的解码值、引号标记与原文字节偏移（半开区间 [Start,End)）。
// Quoted 区分「未引号空字段」与「加引号空字段 ""」。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 比较两个字段的值与引号标记（偏移不参与逻辑相等）。
func (c Cell) Equal(o Cell) bool { return c.Quoted == o.Quoted && c.Value == o.Value }
