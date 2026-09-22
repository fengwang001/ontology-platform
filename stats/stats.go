package stats

// DefaultSelectivity 是列统计缺失时回退的写死选择率。
const DefaultSelectivity = 0.1

// Column 是单列统计：相异值个数 NDV 与可选的等宽直方图。
// Hist 为 nil 表示该列没有直方图（允许，不算损坏）。
type Column struct {
	Name string
	NDV  int64
	Hist *Histogram
}

// Table 是一张表的统计快照。Rows 为快照声称的行数。
type Table struct {
	Name    string
	Rows    int64
	Columns map[string]*Column
}

// ColumnByName 返回列统计，未登记时返回 nil。
func (t *Table) ColumnByName(name string) *Column {
	if t.Columns == nil {
		return nil
	}
	return t.Columns[name]
}

// Selectivity 估计两个等号连接列的选择率 = 1/max(NDV1,NDV2)。
// 任一列统计缺失（nil 或 NDV<=0）时回退 DefaultSelectivity，
// 第二个返回值 missing 报告是否发生了回退。
func Selectivity(c1, c2 *Column) (sel float64, missing bool) {
	if c1 == nil || c2 == nil || c1.NDV <= 0 || c2.NDV <= 0 {
		return DefaultSelectivity, true
	}
	hi := c1.NDV
	if c2.NDV > hi {
		hi = c2.NDV
	}
	return 1.0 / float64(hi), false
}
