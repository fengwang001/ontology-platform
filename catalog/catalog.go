package catalog

import (
	"errors"
	"fmt"
	"sync"

	"ontology/stats"
)

var (
	// ErrSelfJoin 表示谓词两侧引用同一张表，本版明确拒绝。
	ErrSelfJoin = errors.New("catalog: self-join is not supported")
)

// StaleFraction 是统计过期阈值：行数偏差超过 10% 即标注。
const StaleFraction = 0.1

// Entry 是一张表的目录登记：权威行数 + 统计快照。
type Entry struct {
	Rows  int64
	Stats *stats.Table
}

// Predicate 是一个等值连接谓词 LeftTable.LeftCol = RightTable.RightCol。
type Predicate struct {
	LeftTable, LeftCol   string
	RightTable, RightCol string
}

// Issue 记录一处非致命统计问题（缺失/过期），供 explain 逐节点标注。
type Issue struct {
	Table, Column string
	Err           error
}

// Catalog 是表与谓词的登记处。注册完成后视为只读，可被多协程共享。
type Catalog struct {
	mu       sync.RWMutex
	tables   map[string]*Entry
	order    []string
	preds    []Predicate
	rowReads int64
}

func New() *Catalog {
	return &Catalog{tables: map[string]*Entry{}}
}

func (c *Catalog) AddTable(name string, rows int64, st *stats.Table) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, dup := c.tables[name]; dup {
		return fmt.Errorf("catalog: duplicate table %q", name)
	}
	c.tables[name] = &Entry{Rows: rows, Stats: st}
	c.order = append(c.order, name)
	return nil
}

func (c *Catalog) AddPredicate(p Predicate) error {
	if p.LeftTable == p.RightTable {
		return fmt.Errorf("%w: table %q", ErrSelfJoin, p.LeftTable)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, q := range c.preds {
		if p == q {
			return fmt.Errorf("catalog: duplicate predicate %+v", p)
		}
	}
	c.preds = append(c.preds, p)
	return nil
}

// Validate 检查全部统计。损坏是致命错误；缺失/过期作为 Issue 返回，不中止。
// 返回的 Issue 的 Err 可被 errors.Is 判为 stats.ErrMissingStats / stats.ErrStaleStats。
func (c *Catalog) Validate() ([]Issue, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var issues []Issue
	for _, name := range c.order {
		e := c.tables[name]
		if e.Stats == nil {
			return nil, fmt.Errorf("%w: table %q has no statistics", stats.ErrMissingStats, name)
		}
		st := e.Stats
		if err := st.Validate(); err != nil {
			return nil, err
		}
		diff := float64(e.Rows - st.Rows)
		if diff < 0 {
			diff = -diff
		}
		if diff > StaleFraction*float64(maxInt64(st.Rows, 1)) {
			issues = append(issues, Issue{Table: name,
				Err: fmt.Errorf("%w: table %q catalog rows %d != stats rows %d",
					stats.ErrStaleStats, name, e.Rows, st.Rows)})
		}
		for colName, col := range st.Columns {
			if col == nil {
				issues = append(issues, Issue{Table: name, Column: colName,
					Err: fmt.Errorf("%w: table %q column %q", stats.ErrMissingStats, name, colName)})
			}
		}
	}
	return issues, nil
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// Snapshot 返回注册顺序的表名、表登记与谓词副本（只读调用用）。
func (c *Catalog) Snapshot() ([]string, map[string]*Entry, []Predicate) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	names := append([]string(nil), c.order...)
	preds := append([]Predicate(nil), c.preds...)
	tables := make(map[string]*Entry, len(c.tables))
	for k, v := range c.tables {
		tables[k] = v
	}
	return names, tables, preds
}

// ReadRows 模拟「读取一行数据」。代价/基数估计绝不应触发它；测试用它断言零行访问。
func (c *Catalog) ReadRows(table string, n int64) {
	c.mu.Lock()
	c.rowReads += n
	c.mu.Unlock()
}

func (c *Catalog) RowReadCount() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.rowReads
}
