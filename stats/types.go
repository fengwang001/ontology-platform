package stats

import "fmt"

// DefaultSelectivity 是列统计缺失时回退的写死选择率（1/10）。
const DefaultSelectivity = 0.1

// Column 是单列统计。HasStats=false（或 Column 为 nil）表示该列完全无统计。
type Column struct {
	Name     string
	NDV      int64
	Hist     *Histogram
	HasStats bool
}

// TableStats 是一张表的统计快照。Rows 是统计声称的行数，可能已过期。
type TableStats struct {
	Table string
	Rows  int64
	Pages int64
	Cols  map[string]*Column
}

// Note 记录一次估计中触发的可解释标注。
type Note struct {
	Missing     bool
	Stale       bool
	Independent bool
	Text        string
}

// Estimator 只基于登记的统计对象做估计。statsLookups 统计读取统计的次数；
// rowDataReads 永远为 0——本类型没有任何途径触达行数据。
type Estimator struct {
	tables       map[string]*TableStats
	catalogRows  func(string) int64
	statsLookups int64
	rowDataReads int64
}

// NewEstimator 构造估计器。catalogRows 返回目录权威行数，用于过期校正。
func NewEstimator(tables map[string]*TableStats, catalogRows func(string) int64) *Estimator {
	if catalogRows == nil {
		catalogRows = func(string) int64 { return 0 }
	}
	return &Estimator{tables: tables, catalogRows: catalogRows}
}

// StatsLookups 返回估计过程中读取统计对象的次数。
func (e *Estimator) StatsLookups() int64 { return e.statsLookups }

// RowDataReads 返回估计过程中访问行数据的次数，恒为 0。
func (e *Estimator) RowDataReads() int64 { return e.rowDataReads }

func (e *Estimator) lookup(table, col string) (*TableStats, *Column, []Note, error) {
	tab, ok := e.tables[table]
	if !ok {
		return nil, nil, nil, fmt.Errorf("%w: table %q", ErrStatMissing, table)
	}
	e.statsLookups++
	var notes []Note
	catRows := e.catalogRows(table)
	if catRows > 0 {
		diff := catRows - tab.Rows
		if diff < 0 {
			diff = -diff
		}
		if float64(diff)/float64(catRows) > StaleFraction {
			notes = append(notes, Note{Stale: true,
				Text: fmt.Sprintf("统计已过期：表 %s 按目录行数 %d 校正（统计值 %d）", table, catRows, tab.Rows)})
		}
	}
	column := tab.Cols[col]
	if column == nil || !column.HasStats {
		notes = append(notes, Note{Missing: true,
			Text: fmt.Sprintf("统计缺失：表 %s 列 %s 估计不可靠（回退选择率 %g）", table, col, DefaultSelectivity)})
		return tab, nil, notes, nil
	}
	if column.Hist != nil {
		if err := column.Hist.Validate(tab.Rows); err != nil {
			return nil, nil, nil, fmt.Errorf("%w: 表 %s 列 %s", err, table, col)
		}
	}
	return tab, column, notes, nil
}
