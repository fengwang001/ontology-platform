// Package catalog 维护列登记与人工定级：每列的固有级别 base、
// 下限 floor 与经审批的临时上限 cap。上限的失效采取惰性策略，
// 由调用方在接受变更操作时以 ExpireCaps 落地。
package catalog

import "sort"

const (
	// MaxNameLen 列名最大字节数。
	MaxNameLen = 128
	// MaxLevel 敏感级别上限（0 公开，4 绝密）。
	MaxLevel = 4
)

// ValidName 报告列名是否为 1 到 128 字节的非空字节串。
func ValidName(name string) bool { return len(name) >= 1 && len(name) <= MaxNameLen }

// ValidLevel 报告级别是否在 0 到 4 之间。
func ValidLevel(l int) bool { return l >= 0 && l <= MaxLevel }

// Column 是一列的登记信息与人工定级。
type Column struct {
	Name     string
	Base     int   // 固有级别
	Floor    int   // 下限，0 表示未设
	Cap      int   // 临时上限
	CapUntil int64 // cap 生效区间为上开终点：t < CapUntil 生效
	CapSet   bool
}

// Catalog 是列登记表。不是并发安全的，由上层（classify.Engine）串行化。
type Catalog struct {
	nmax int
	cols map[string]*Column
}

// New 创建列数上限为 nmax 的空登记表。
func New(nmax int) *Catalog {
	return &Catalog{nmax: nmax, cols: make(map[string]*Column)}
}

// NMax 返回列数上限。
func (c *Catalog) NMax() int { return c.nmax }

// Len 返回已登记列数。
func (c *Catalog) Len() int { return len(c.cols) }

// Has 报告列是否存在。
func (c *Catalog) Has(name string) bool {
	_, ok := c.cols[name]
	return ok
}

// Get 返回列，不存在时返回 nil。返回值为内部对象，调用方不得长期持有。
func (c *Catalog) Get(name string) *Column { return c.cols[name] }

// Add 登记新列，调用方需保证名字合法且不重复。
func (c *Catalog) Add(name string, base int) {
	c.cols[name] = &Column{Name: name, Base: base}
}

// SetBase 修改固有级别。
func (c *Catalog) SetBase(name string, base int) { c.cols[name].Base = base }

// SetFloor 设置下限，l 为 0 表示取消。
func (c *Catalog) SetFloor(name string, l int) { c.cols[name].Floor = l }

// SetCap 设置（或覆盖）临时上限。
func (c *Catalog) SetCap(name string, l int, until int64) {
	col := c.cols[name]
	col.Cap, col.CapUntil, col.CapSet = l, until, true
}

// CapActive 报告列的临时上限在时刻 now 是否生效（t < until 生效）。
func (c *Catalog) CapActive(name string, now int64) bool {
	col := c.cols[name]
	return col.CapSet && col.CapUntil > now
}

// ExpireCaps 让所有 until <= now 的上限失效，返回失效列名（顺序不定）。
func (c *Catalog) ExpireCaps(now int64) []string {
	var expired []string
	for name, col := range c.cols {
		if col.CapSet && col.CapUntil <= now {
			col.CapSet = false
			expired = append(expired, name)
		}
	}
	return expired
}

// Names 按字节序升序返回全部列名。
func (c *Catalog) Names() []string {
	names := make([]string, 0, len(c.cols))
	for name := range c.cols {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
