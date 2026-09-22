package catalog

import (
	"errors"
	"fmt"

	"ontology/stats"
)

// Stale 判断表统计是否过期：|statRows-catRows| > StaleFraction·catRows。
// catRows 为 0 时，只要统计行数非 0 即过期。
func Stale(t *Table) bool {
	if t.Stat == nil {
		return false
	}
	diff := t.Stat.Rows - t.Rows
	if diff < 0 {
		diff = -diff
	}
	if t.Rows == 0 {
		return diff > 0
	}
	return float64(diff) > StaleFraction*float64(t.Rows)
}

// TableWarnings 返回该表的非致命警告（当前仅过期）。
func TableWarnings(t *Table) []Warning {
	if Stale(t) {
		return []Warning{{
			Kind: ErrStaleStats,
			Text: fmt.Sprintf("表 %s 统计已过期：统计行数 %d，目录行数 %d，按目录行数校正",
				t.Name, t.Stat.Rows, t.Rows),
		}}
	}
	return nil
}

// Resolve 解析谓词两侧列统计，返回缺失警告（不中止，调用方回退默认选择率）。
// 缺统计的列在返回的 *stats.Column 中为 nil。
func (c *Catalog) Resolve(p *Predicate) (*stats.Column, *stats.Column, []Warning) {
	var warns []Warning
	resolve := func(table, col string) *stats.Column {
		t := c.TableByName(table)
		if t == nil || t.Stat == nil {
			warns = append(warns, Warning{
				Kind: ErrMissingStats,
				Text: fmt.Sprintf("表 %s 列 %s 无统计：估计不可靠（默认选择率 %g）",
					table, col, stats.DefaultSelectivity),
			})
			return nil
		}
		column := t.Stat.ColumnByName(col)
		if column == nil {
			warns = append(warns, Warning{
				Kind: ErrMissingStats,
				Text: fmt.Sprintf("表 %s 列 %s 无统计：估计不可靠（默认选择率 %g）",
					table, col, stats.DefaultSelectivity),
			})
		}
		return column
	}
	c1 := resolve(p.LeftTable, p.LeftCol)
	c2 := resolve(p.RightTable, p.RightCol)
	return c1, c2, warns
}

// HasWarningKind 报告警告集合中是否存在某类异常（errors.Is 判定）。
func HasWarningKind(ws []Warning, kind error) bool {
	for _, w := range ws {
		if errors.Is(w.Kind, kind) {
			return true
		}
	}
	return false
}
