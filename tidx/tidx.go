// Package tidx 维护只追加日志的时间索引 (TS, Offset)。
// 索引项时间戳严格递增；Lookup 在索引上二分，不扫描消息。
package tidx

import (
	"sort"
	"sync/atomic"
)

// Entry 是一条时间索引项：时间戳 ts 处首次出现新最大值时的位点。
type Entry struct {
	TS     int64
	Offset int64
}

// Index 是时间索引。零值不可直接用，请用 New。
type Index struct {
	items []Entry
	// probes 记录最近一次 Lookup 探查过的索引项个数。
	// 非导出字段，不进公开接口；用原子类型以承受并发只读查询。
	probes atomic.Int64
}

// New 返回空时间索引。
func New() *Index { return &Index{} }

// Add 按规则追加索引项：仅当 ts 严格大于此前全部项的最大时间戳时记录，
// 返回是否真的追加。调用方保证 off 随消息位点严格递增。
func (ix *Index) Add(ts, off int64) bool {
	if n := len(ix.items); n > 0 && ts <= ix.items[n-1].TS {
		return false
	}
	ix.items = append(ix.items, Entry{TS: ts, Offset: off})
	return true
}

// Lookup 返回时间戳 >= t 的第一个索引项的位点。
// 找不到（含空索引）返回 (0, false)，由上层把位点替换为 LEO。
func (ix *Index) Lookup(t int64) (off int64, found bool) {
	n := 0
	i := sort.Search(len(ix.items), func(i int) bool {
		n++
		return ix.items[i].TS >= t
	})
	ix.probes.Store(int64(n))
	if i == len(ix.items) {
		return 0, false
	}
	return ix.items[i].Offset, true
}

// ProbeWithinBound 只以布尔结论报告：最近一次 Lookup 的探查数是否不超过
// 2*ceil(log2(m+1))+4（m 为索引项数）。探查次数本身不离开本包，
// 供同模块 demo/自检以结论方式核验二分上界。
func (ix *Index) ProbeWithinBound() bool {
	m := len(ix.items)
	bound := 2*ceilLog2(m+1) + 4
	return int(ix.probes.Load()) <= bound
}

// Entries 返回索引项的副本快照。
func (ix *Index) Entries() []Entry {
	out := make([]Entry, len(ix.items))
	copy(out, ix.items)
	return out
}

func ceilLog2(x int) int {
	k := 0
	for p := 1; p < x; p <<= 1 {
		k++
	}
	return k
}
