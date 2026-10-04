// Package catalog 维护列登记与人工定级（固有级别、下限、临时上限）。
package catalog

import "sort"

// Cap 表示经审批的临时上限：Level 在 t < Until 时生效。
type Cap struct {
	Level int
	Until int64
}

// Column 是一列的登记信息。
type Column struct {
	Name  string
	Base  int
	Floor int
	Cap   *Cap
}

// Catalog 保存所有列的登记信息，非并发安全，由上层加锁。
type Catalog struct {
	nmax    int
	columns map[string]*Column
}

// New 创建容量上限为 nmax 的登记表。
func New(nmax int) *Catalog {
	return &Catalog{nmax: nmax, columns: make(map[string]*Column)}
}

// Nmax 返回列数上限。
func (c *Catalog) Nmax() int { return c.nmax }

// Len 返回已登记列数。
func (c *Catalog) Len() int { return len(c.columns) }

// Has 报告列是否已登记。
func (c *Catalog) Has(col string) bool {
	_, ok := c.columns[col]
	return ok
}

// Get 返回列指针，不存在返回 nil。
func (c *Catalog) Get(col string) *Column { return c.columns[col] }

// Add 登记新列；已存在时返回 false。
func (c *Catalog) Add(col string, base int) bool {
	if c.Has(col) {
		return false
	}
	c.columns[col] = &Column{Name: col, Base: base}
	return true
}

// SetBase 修改固有级别。
func (c *Catalog) SetBase(col string, base int) { c.columns[col].Base = base }

// SetFloor 设置下限（0 即取消）。
func (c *Catalog) SetFloor(col string, level int) { c.columns[col].Floor = level }

// SetCap 设置临时上限；nil 表示取消。
func (c *Catalog) SetCap(col string, cap *Cap) { c.columns[col].Cap = cap }

// ExpireCaps 移除所有 until <= now 的上限，返回受影响列名（字节序升序）。
func (c *Catalog) ExpireCaps(now int64) []string {
	var affected []string
	for name, col := range c.columns {
		if col.Cap != nil && col.Cap.Until <= now {
			col.Cap = nil
			affected = append(affected, name)
		}
	}
	sort.Strings(affected)
	return affected
}
