package stats

import (
	"errors"
	"fmt"
)

// 统计异常哨兵错误，调用方可用 errors.Is 区分。
var (
	ErrMissingStats = errors.New("stats: missing column statistics")
	ErrStaleStats   = errors.New("stats: statistics are stale")
	ErrCorruptStats = errors.New("stats: histogram is corrupt")
)

// Histogram 是等宽直方图：第 i 个桶覆盖 (prev, UpperBounds[i]]，计数 Counts[i]。
type Histogram struct {
	Width       float64
	UpperBounds []float64
	Counts      []int64
}

// Column 是单列统计。指针为 nil 即表示该列没有统计（缺失异常）。
type Column struct {
	Name string
	NDV  int64
	Hist *Histogram
}

// Table 是一张表的统计快照。Rows 为统计声称的行数。
type Table struct {
	Name    string
	Rows    int64
	Columns map[string]*Column
}

// Validate 检查直方图结构：桶计数之和必须等于声称行数，桶边界必须严格递增。
func (t *Table) Validate() error {
	for colName, col := range t.Columns {
		h := col.Hist
		if h == nil {
			continue
		}
		if len(h.UpperBounds) != len(h.Counts) {
			return fmt.Errorf("%w: table %q column %q: bounds/counts length mismatch",
				ErrCorruptStats, t.Name, colName)
		}
		var sum int64
		for i, c := range h.Counts {
			if c < 0 {
				return fmt.Errorf("%w: table %q column %q: negative bucket count",
					ErrCorruptStats, t.Name, colName)
			}
			sum += c
			if i > 0 && h.UpperBounds[i] <= h.UpperBounds[i-1] {
				return fmt.Errorf("%w: table %q column %q: bounds not strictly increasing",
					ErrCorruptStats, t.Name, colName)
			}
		}
		if sum != t.Rows {
			return fmt.Errorf("%w: table %q column %q: bucket sum %d != rows %d",
				ErrCorruptStats, t.Name, colName, sum, t.Rows)
		}
	}
	return nil
}

// ColumnStat 返回某列统计；列不存在时返回包装过的 ErrMissingStats。
func (t *Table) ColumnStat(name string) (*Column, error) {
	if c, ok := t.Columns[name]; ok && c != nil {
		return c, nil
	}
	return nil, fmt.Errorf("%w: table %q column %q", ErrMissingStats, t.Name, name)
}
