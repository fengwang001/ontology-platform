// Package stats 提供表与列的统计信息（行数、相异值个数、等宽直方图）
// 以及统计信息自身的完整性校验。
package stats

import (
	"errors"
	"fmt"
)

// ErrCorruptStats 表示统计信息损坏（直方图桶计数与总行数不符，或桶边界非递增）。
var ErrCorruptStats = errors.New("stats: corrupt statistics")

// Histogram 是等宽直方图：Bounds 为桶边界（长度 = len(Counts)+1，严格递增），
// Counts 为每个桶内的行数。
type Histogram struct {
	Bounds []float64
	Counts []uint64
}

// validate 校验直方图：边界严格递增，且桶计数之和等于 total 行。
func (h *Histogram) validate(table, column string, total uint64) error {
	if len(h.Bounds) != len(h.Counts)+1 {
		return fmt.Errorf("%w: table %s column %s: %d bounds for %d buckets",
			ErrCorruptStats, table, column, len(h.Bounds), len(h.Counts))
	}
	for i := 1; i < len(h.Bounds); i++ {
		if h.Bounds[i] <= h.Bounds[i-1] {
			return fmt.Errorf("%w: table %s column %s: bucket bounds not increasing at %d",
				ErrCorruptStats, table, column, i)
		}
	}
	var sum uint64
	for _, c := range h.Counts {
		sum += c
	}
	if sum != total {
		return fmt.Errorf("%w: table %s column %s: bucket counts sum %d != rows %d",
			ErrCorruptStats, table, column, sum, total)
	}
	return nil
}

// ColumnStats 是单列统计：相异值个数（NDV）与可选的等宽直方图。
type ColumnStats struct {
	Name string
	NDV  uint64
	Hist *Histogram
}

// TableStats 是一张某表的统计：声称的行数与各列统计。
type TableStats struct {
	Table   string
	Rows    uint64
	Columns map[string]*ColumnStats
}

// Column 返回某列统计，缺失时返回 nil。
func (t *TableStats) Column(name string) *ColumnStats {
	if t == nil {
		return nil
	}
	return t.Columns[name]
}

// Validate 校验表统计的完整性；损坏时返回包装 ErrCorruptStats 的错误，
// 消息中含表名与列名。
func (t *TableStats) Validate() error {
	if t == nil {
		return nil
	}
	for _, col := range t.Columns {
		if col.Hist != nil {
			if err := col.Hist.validate(t.Table, col.Name, t.Rows); err != nil {
				return err
			}
		}
	}
	return nil
}
