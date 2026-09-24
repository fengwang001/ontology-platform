// Package idx 维护稀疏索引条目：int32 相对位点 + 物理位置。
package idx

import "sort"

// Entry 是一条索引条目：相对位点（段基位点为 0）与对应记录的物理位置。
type Entry struct {
	Rel int32
	Pos int64
}

// Index 是按相对位点严格递增的条目序列。
type Index struct {
	entries []Entry
}

// Add 追加一条条目；调用方保证相对位点与物理位置均严格递增。
func (x *Index) Add(rel int32, pos int64) {
	x.entries = append(x.entries, Entry{Rel: rel, Pos: pos})
}

// Floor 返回相对位点 <= rel 的最后一个条目；checked 为二分过程中检查过的条目数。
func (x *Index) Floor(rel int32) (Entry, bool, int) {
	n := len(x.entries)
	i := sort.Search(n, func(i int) bool { return x.entries[i].Rel > rel })
	if i == 0 {
		return Entry{}, false, ceilLog2(n + 1)
	}
	return x.entries[i-1], true, ceilLog2(n + 1)
}

// Entries 返回全部条目的副本。
func (x *Index) Entries() []Entry {
	out := make([]Entry, len(x.entries))
	copy(out, x.entries)
	return out
}

func ceilLog2(n int) int {
	c := 0
	for (1 << c) < n {
		c++
	}
	return c
}
