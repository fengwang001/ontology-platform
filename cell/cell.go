// Package cell 表示 CSV 字段值：内容、是否原文加引号、原文起止字节偏移。
package cell

// Cell 是一个字段的解析结果。
type Cell struct {
	Value  string // 反转义后的字段内容（引号字段内 \r\n 原样保留）
	Quoted bool   // 原文是否由引号包裹；可区分裸空字段与 ""
	Start  int64  // 字段首字节在原文流中的偏移（从 0 起）
	End    int64  // 字段末字节之后的偏移（半开区间 [Start,End)）
}

// String 用于演示与调试：引号字段显示带引号形态。
func (c Cell) String() string {
	if c.Quoted {
		return "\"" + c.Value + "\""
	}
	return c.Value
}

// Equal 报告两个字段逐位相等（内容与引号标记都相同；偏移不参与）。
func (c Cell) Equal(o Cell) bool { return c.Quoted == o.Quoted && c.Value == o.Value }
