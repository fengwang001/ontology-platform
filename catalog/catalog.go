// Package catalog 登记表、连接谓词与统计信息，并在分析时检测
// 统计信息的缺失、过期与损坏三种异常。
package catalog

import (
	"errors"
	"fmt"

	"ontology/stats"
)

// 三种统计异常与边界错误的可判定哨兵。
var (
	ErrMissingStats = errors.New("catalog: missing column statistics")
	ErrStaleStats   = errors.New("catalog: stale statistics")
	ErrSelfJoin     = errors.New("catalog: self-join predicate not supported")
	ErrUnknownTable = errors.New("catalog: unknown table")
)

// StaleThreshold 是判定统计过期的相对行数偏差阈值（20%）。
const StaleThreshold = 0.2

// ColRef 引用某表的某列。
type ColRef struct {
	Table  string
	Column string
}

func (c ColRef) String() string { return c.Table + "." + c.Column }

// Predicate 是连接两列的等值谓词。
type Predicate struct {
	Left  ColRef
	Right ColRef
}

// Catalog 是表、谓词与统计信息的登记表。
type Catalog struct {
	rows  map[string]uint64
	order []string
	preds []Predicate
	stats map[string]*stats.TableStats
}

// New 创建空目录。
func New() *Catalog {
	return &Catalog{rows: map[string]uint64{}, stats: map[string]*stats.TableStats{}}
}

// AddTable 登记一张表及其目录行数。
func (c *Catalog) AddTable(name string, rows uint64) {
	if _, ok := c.rows[name]; !ok {
		c.order = append(c.order, name)
	}
	c.rows[name] = rows
}

// AddPredicate 登记一个等值连接谓词；自连接谓词被拒绝。
func (c *Catalog) AddPredicate(left, right ColRef) error {
	if left.Table == right.Table {
		return fmt.Errorf("%w: %s = %s", ErrSelfJoin, left, right)
	}
	for _, ref := range []ColRef{left, right} {
		if _, ok := c.rows[ref.Table]; !ok {
			return fmt.Errorf("%w: %s", ErrUnknownTable, ref.Table)
		}
	}
	c.preds = append(c.preds, Predicate{Left: left, Right: right})
	return nil
}

// SetStats 登记某表的统计信息。
func (c *Catalog) SetStats(ts *stats.TableStats) { c.stats[ts.Table] = ts }

// TableInfo 是分析后的表视图：有效行数（过期时已按目录校正）。
type TableInfo struct {
	Name  string
	Rows  float64
	Stale bool
}

// PredInfo 是分析后的谓词视图：选择率与是否因缺统计而不可靠。
type PredInfo struct {
	Pred       Predicate
	Sel        float64
	Unreliable bool
}

// View 是供规划器使用的只读分析结果。
type View struct {
	Tables []TableInfo
	Preds  []PredInfo
}

// Analyze 校验统计并生成规划视图。损坏的统计导致返回硬错误；
// 缺失与过期只产生警告（可用 errors.Is 判定），不中止。
func (c *Catalog) Analyze() (*View, []error, error) {
	var warnings []error
	for _, ts := range c.stats {
		if err := ts.Validate(); err != nil {
			return nil, nil, err
		}
	}
	v := &View{}
	for _, name := range c.order {
		info := TableInfo{Name: name, Rows: float64(c.rows[name])}
		if ts := c.stats[name]; ts != nil {
			cat := c.rows[name]
			dev := float64(ts.Rows) - float64(cat)
			if dev < 0 {
				dev = -dev
			}
			if denom := float64(cat); denom > 0 && dev/denom > StaleThreshold {
				info.Stale = true
				warnings = append(warnings, fmt.Errorf("%w: table %s: stats rows %d vs catalog rows %d, corrected to catalog",
					ErrStaleStats, name, ts.Rows, cat))
			}
		}
		v.Tables = append(v.Tables, info)
	}
	for _, p := range c.preds {
		pi := PredInfo{Pred: p}
		ndvL, okL := c.ndv(p.Left)
		ndvR, okR := c.ndv(p.Right)
		if !okL || !okR {
			pi.Sel = stats.DefaultSelectivity
			pi.Unreliable = true
			for _, miss := range []struct {
				ref ColRef
				ok  bool
			}{{p.Left, okL}, {p.Right, okR}} {
				if !miss.ok {
					warnings = append(warnings, fmt.Errorf("%w: %s, fallback selectivity %.3f",
						ErrMissingStats, miss.ref, stats.DefaultSelectivity))
				}
			}
		} else {
			pi.Sel = stats.EqJoinSelectivity(ndvL, ndvR)
		}
		v.Preds = append(v.Preds, pi)
	}
	return v, warnings, nil
}

// ndv 返回某列的相异值个数；列无统计时 ok=false。
func (c *Catalog) ndv(ref ColRef) (uint64, bool) {
	ts := c.stats[ref.Table]
	if ts == nil {
		return 0, false
	}
	col := ts.Column(ref.Column)
	if col == nil {
		return 0, false
	}
	return col.NDV, true
}
