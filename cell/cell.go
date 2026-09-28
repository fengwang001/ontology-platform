// Package cell 表示 CSV 记录中的单个字段。
package cell

// Cell 是一个字段的解析结果。
//
// Value 是字段逻辑值（引号已剥离，"" 已还原为一个 "）。
// Quoted 区分加引号的空字段（""）与未加引号的空字段。
// Start/End 是该字段在原始输入中的字节半开区间 [Start,End)：
// 加引号字段包含首尾引号；未加引号空字段为一个零宽位置
// （指向其后的分隔符/行尾所在偏移）。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Equal 报告两个字段逐位相同（值与引号标记都一致）。
func (c Cell) Equal(o Cell) bool {
	return c.Quoted == o.Quoted && c.Value == o.Value
}

// EqualDeep 还比较原始偏移，用于并行/流式一致性校验。
func (c Cell) EqualDeep(o Cell) bool {
	return c.Equal(o) && c.Start == o.Start && c.End == o.End
}
