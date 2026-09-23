package cell

// Cell 表示一个 CSV 字段：解码后的内容、原文字节区间、是否带引号。
// Quoted=false 的空字段与 Quoted=true（""）可区分。
type Cell struct {
	Value  []byte
	Quoted bool
	Off    int // 首内容字节偏移；空字段为分隔符/行尾所在位置
	End    int // 末内容字节偏移+1；空字段等于 Off
}

// Row 是一条记录（表头也是一行）。
type Row []Cell

// Doc 是解析结果：Header 为第一条记录，Rows 为其余记录。
type Doc struct {
	Header Row
	Rows   []Row
}

// All 依次返回表头与所有数据行。
func (d *Doc) All() []Row {
	out := make([]Row, 0, len(d.Rows)+1)
	if d.Header != nil {
		out = append(out, d.Header)
	}
	out = append(out, d.Rows...)
	return out
}

// Limits 为三类资源上限；0 表示不限制。
type Limits struct {
	MaxFieldBytes int // 单字段最大逻辑字符数
	MaxFields     int // 单记录最大字段数
	MaxRecords    int // 总记录数上限（含表头）
}
