package catalog

import (
	"fmt"

	"ontology/stats"
)

// Table 是目录登记项：当前行数 Rows 与一份统计快照 Stat。
type Table struct {
	Name string
	Rows int64
	Stat *stats.Table
}

// Predicate 是形如 LeftTable.LeftCol = RightTable.RightCol 的等号连接谓词。
type Predicate struct {
	LeftTable, LeftCol   string
	RightTable, RightCol string
}

// Warning 附着在计划上的非致命统计异常，供 explain 标注。
type Warning struct {
	Kind error // ErrMissingStats / ErrStaleStats
	Text string
}

// Catalog 是表与谓词的登记表。构造后只读，可被多协程并发使用。
type Catalog struct {
	tables []*Table
	byName map[string]*Table
	preds  []*Predicate
}

// New 创建空目录。
func New() *Catalog {
	return &Catalog{byName: map[string]*Table{}}
}

// Register 登记一张表并做损坏检测：直方图不自洽返回 ErrCorruptStats
// （错误信息指出表名与列名）。过期只检查并记录，不阻止登记。
func (c *Catalog) Register(t *Table) error {
	if t.Stat != nil {
		for colName, col := range t.Stat.Columns {
			if col.Hist != nil {
				if err := col.Hist.Validate(t.Stat.Rows); err != nil {
					return fmt.Errorf("table %q column %q: %w", t.Name, colName, ErrCorruptStats)
				}
			}
		}
	}
	c.tables = append(c.tables, t)
	c.byName[t.Name] = t
	return nil
}

// AddPredicate 登记谓词；同表两侧返回 ErrSelfJoin。
func (c *Catalog) AddPredicate(p *Predicate) error {
	if p.LeftTable == p.RightTable {
		return fmt.Errorf("predicate %s.%s=%s.%s: %w",
			p.LeftTable, p.LeftCol, p.RightTable, p.RightCol, ErrSelfJoin)
	}
	c.preds = append(c.preds, p)
	return nil
}

// Tables 按登记顺序返回表。
func (c *Catalog) Tables() []*Table { return c.tables }

// Predicates 返回全部谓词。
func (c *Catalog) Predicates() []*Predicate { return c.preds }

// TableByName 按名取表。
func (c *Catalog) TableByName(name string) *Table { return c.byName[name] }
