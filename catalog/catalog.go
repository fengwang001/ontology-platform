// Package catalog 维护列登记簿：列名、固有级别 base、人工定级
// floor（下限）与 cap/until（经审批的临时上限），以及上限的惰性失效。
package catalog

import (
	"errors"
	"fmt"
	"sort"
)

// 细粒度哨兵错误，均可由 errors.Is 区分；classify 包会把它们归入
// 更高层的错误类别。
var (
	ErrInvalidName  = errors.New("catalog: invalid column name")
	ErrInvalidLevel = errors.New("catalog: invalid level")
	ErrInvalidNmax  = errors.New("catalog: invalid nmax")
	ErrNotFound     = errors.New("catalog: column not found")
	ErrExists       = errors.New("catalog: column already exists")
	ErrLimit        = errors.New("catalog: column count limit exceeded")
)

const (
	// MinLevel 与 MaxLevel 是敏感级别的闭区间边界（0 公开，4 绝密）。
	MinLevel = 0
	MaxLevel = 4
	// MaxNameLen 是列名最大字节数。
	MaxNameLen = 128
	// MaxNmax 是列数上限 Nmax 的最大取值。
	MaxNmax = 100000
)

// ValidName 报告列名是否为 1 到 128 字节的非空字节串。
func ValidName(name string) bool {
	return len(name) >= 1 && len(name) <= MaxNameLen
}

// ValidLevel 报告级别是否在 [0,4] 内。
func ValidLevel(l int) bool {
	return l >= MinLevel && l <= MaxLevel
}

// Column 是一列的登记信息。
type Column struct {
	Name   string
	Base   int
	Floor  int // 0 表示未设下限
	Cap    int // 仅 HasCap 为真时有效
	Until  int64
	HasCap bool
}

// Catalog 是列登记簿。不是并发安全的，由上层引擎串行化访问。
type Catalog struct {
	nmax int
	cols map[string]*Column
}

// New 创建列数上限为 nmax 的登记簿，nmax 须在 [1, 1e5] 内。
func New(nmax int) (*Catalog, error) {
	if nmax < 1 || nmax > MaxNmax {
		return nil, fmt.Errorf("%w: %d", ErrInvalidNmax, nmax)
	}
	return &Catalog{nmax: nmax, cols: make(map[string]*Column)}, nil
}

// Nmax 返回列数上限。
func (c *Catalog) Nmax() int { return c.nmax }

// Len 返回已登记列数。
func (c *Catalog) Len() int { return len(c.cols) }

// Get 按名取列，未登记时 ok 为假。
func (c *Catalog) Get(name string) (col *Column, ok bool) {
	col, ok = c.cols[name]
	return col, ok
}

// Names 返回按字节序升序的全部列名。
func (c *Catalog) Names() []string {
	names := make([]string, 0, len(c.cols))
	for name := range c.cols {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Add 登记新列及其固有级别。
func (c *Catalog) Add(name string, base int) error {
	if !ValidName(name) {
		return fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	if !ValidLevel(base) {
		return fmt.Errorf("%w: %d", ErrInvalidLevel, base)
	}
	if _, ok := c.cols[name]; ok {
		return fmt.Errorf("%w: %q", ErrExists, name)
	}
	if len(c.cols) >= c.nmax {
		return fmt.Errorf("%w: nmax=%d", ErrLimit, c.nmax)
	}
	c.cols[name] = &Column{Name: name, Base: base}
	return nil
}

// SetBase 修改固有级别。
func (c *Catalog) SetBase(name string, base int) error {
	if !ValidLevel(base) {
		return fmt.Errorf("%w: %d", ErrInvalidLevel, base)
	}
	col, ok := c.cols[name]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	col.Base = base
	return nil
}

// SetFloor 设置下限，l 为 0 即取消；重复调用覆盖。
func (c *Catalog) SetFloor(name string, l int) error {
	if !ValidLevel(l) {
		return fmt.Errorf("%w: %d", ErrInvalidLevel, l)
	}
	col, ok := c.cols[name]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	col.Floor = l
	return nil
}

// SetCap 设置临时上限 cap=l 及失效时刻 until；重复调用覆盖。
func (c *Catalog) SetCap(name string, l int, until int64) error {
	if !ValidLevel(l) {
		return fmt.Errorf("%w: %d", ErrInvalidLevel, l)
	}
	col, ok := c.cols[name]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, name)
	}
	col.Cap = l
	col.Until = until
	col.HasCap = true
	return nil
}

// ExpireCaps 让所有 until <= now 的上限失效，返回失效列名（字节序升序）。
func (c *Catalog) ExpireCaps(now int64) []string {
	var expired []string
	for name, col := range c.cols {
		if col.HasCap && col.Until <= now {
			col.HasCap = false
			expired = append(expired, name)
		}
	}
	sort.Strings(expired)
	return expired
}
