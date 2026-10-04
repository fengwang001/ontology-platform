// Package rating 维护费率组（rate group）的当前单价。
package rating

import "sync"

// Table 是线程安全的费率表：rg → 当前单价（每单位用量金额）。
type Table struct {
	mu sync.RWMutex

	price map[int64]int64
}

// NewTable 创建空费率表。
func NewTable() *Table { return &Table{price: make(map[int64]int64)} }

// Set 设置 rg 的当前单价为 price（now 为设置时刻，仅记录不做校验）。
func (t *Table) Set(rg, price, now int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.price[rg] = price
}

// Get 返回 rg 的当前单价；未设置时 ok 为 false。
func (t *Table) Get(rg int64) (price int64, ok bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	price, ok = t.price[rg]
	return
}

// Known 返回 rg 是否已设单价。
func (t *Table) Known(rg int64) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.price[rg]
	return ok
}
