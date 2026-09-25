// Package dedup 维护 ID → last 的去重表，按 maxOpen 淘汰最久未更新的 ID。
package dedup

import "ontology/win"

// Table 是去重表。前置条件由调用方保证：w > 0、maxOpen > 0、id 非空。
type Table struct {
	w       int64
	maxOpen int
	last    map[string]int64
	acc     int64
	dup     int64
	probes  int // 最近一次 Dedup 为定位 ID 检查过的表条目个数（非导出）
}

// New 建一张空表。
func New(w int64, maxOpen int) *Table {
	return &Table{w: w, maxOpen: maxOpen, last: make(map[string]int64)}
}

// Dedup 报告事件是否被接受。重复（含乱序）只计数，不改 last、不触发淘汰。
// 接受新 ID 且表满时，淘汰 last 最小者。
func (t *Table) Dedup(id string, ts int64) bool {
	t.probes = 1 // map 定位：与表规模无关的常数次检查
	last, ok := t.last[id]
	if ok && win.Duplicate(last, ts, t.w) {
		t.dup++
		return false
	}
	if !ok && len(t.last) >= t.maxOpen {
		t.evictOldest()
	}
	t.last[id] = ts
	t.acc++
	return true
}

// evictOldest 淘汰 last 最小的 ID。淘汰只发生在接受新 ID 且表满时，
// 不在 Dedup 的热路径上，线性扫一遍足够。
func (t *Table) evictOldest() {
	victim, min := "", int64(0)
	first := true
	for id, last := range t.last {
		if first || last < min {
			victim, min, first = id, last, false
		}
	}
	delete(t.last, victim)
}

// View 返回当前保留的 ID → last 的副本。
func (t *Table) View() map[string]int64 {
	out := make(map[string]int64, len(t.last))
	for id, last := range t.last {
		out[id] = last
	}
	return out
}

// Accepted 返回累计接受数。
func (t *Table) Accepted() int64 { return t.acc }

// Duplicated 返回累计重复数。
func (t *Table) Duplicated() int64 { return t.dup }
