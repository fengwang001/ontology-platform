// Package mem 实现 memtable：写缓冲、容量阈值、墓碑条目。
// 不依赖其他包。
package mem

import "sort"

// Entry 是一条写：值、全局单调递增序列号、是否墓碑。
type Entry struct {
	Val  int64
	Seq  uint64
	Tomb bool
}

// KV 是导出的键条目对，供冻结成 SSTable 使用。
type KV struct {
	Key string
	E   Entry
}

// Table 是 memtable：有序 map + 容量阈值，满则由调用方触发冻结。
type Table struct {
	max int
	m   map[string]Entry
}

// New 建一个容量为 max 条的 memtable。
func New(max int) *Table { return &Table{max: max, m: make(map[string]Entry)} }

// Full 报告条目数是否已达容量（== max）。
func (t *Table) Full() bool { return len(t.m) >= t.max }

// Len 返回当前条目数。
func (t *Table) Len() int { return len(t.m) }

// Put 写入一条。调用方须先确认 !Full()（写前冻结由上层负责）。
func (t *Table) Put(key string, e Entry) { t.m[key] = e }

// Get 查一条，命中返回条目与 true（墓碑也算命中）。
func (t *Table) Get(key string) (Entry, bool) {
	e, ok := t.m[key]
	return e, ok
}

// Items 按 key 升序导出全部条目（有序 map 的有序性在冻结时体现）。
func (t *Table) Items() []KV {
	out := make([]KV, 0, len(t.m))
	for k, e := range t.m {
		out = append(out, KV{Key: k, E: e})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
