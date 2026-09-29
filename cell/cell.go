package cell

// Cell 表示一个 CSV 字段。Quoted 区分「未引号空字段」与 `""`。
// Off/End 是该字段在原文中的字节区间 [Off, End)：
// 未引号字段即原始字节；引号字段包含首尾引号，内部为原文 `""` 形式。
type Cell struct {
	Value  string
	Quoted bool
	Off    int
	End    int
}

// New 构造一个带原文位置的字段。
func New(value string, quoted bool, off, end int) Cell {
	return Cell{Value: value, Quoted: quoted, Off: off, End: end}
}

// Equal 逐字段比较（值、引号标记、偏移）。
func (c Cell) Equal(o Cell) bool {
	return c.Value == o.Value && c.Quoted == o.Quoted && c.Off == o.Off && c.End == o.End
}

// Record 是一条记录（一行）。
type Record = []Cell
