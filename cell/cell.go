package cell

// Cell 表示一个 CSV 字段：解码后的值、原文引号标记、原文起止字节偏移（左闭右开）。
type Cell struct {
	Value  string
	Quoted bool
	Start  int64
	End    int64
}

// Row 是一条记录。
type Row = []Cell

// New 构造一个字段。
func New(value string, quoted bool, start, end int64) Cell {
	return Cell{Value: value, Quoted: quoted, Start: start, End: end}
}
