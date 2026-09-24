// Package tidx 维护 (TS, Offset) 时间索引：仅当新消息的时间戳严格大于
// 迄今最大时间戳时追加索引项，并在索引上二分定位。不依赖其他包。
package tidx

import (
	"sort"
	"sync/atomic"
)

// Entry 是一条索引项：TS 为截至 Off 的全部消息时间戳的最大值。
type Entry struct {
	TS  int64
	Off int64
}

// Index 是只追加的时间索引。零值即可用。
type Index struct {
	entries []Entry
	max     int64
	hasMax  bool
	probes  atomic.Int64 // 最近一次 Lookup 探查过的索引项个数（非导出，不外泄数值）
}

// Add 按规则尝试记录一条消息：仅当其 TS 严格大于迄今最大 TS
// （首条消息必记）才追加索引项。
func (x *Index) Add(ts, off int64) {
	if !x.hasMax || ts > x.max {
		x.entries = append(x.entries, Entry{TS: ts, Off: off})
		x.max = ts
		x.hasMax = true
	}
}

// Lookup 返回索引中第一个 TS >= t 的索引项位点；无则 found=false。
// 索引项 TS 严格递增，二分结果即「全日志中第一条 TS >= t 的消息」。
func (x *Index) Lookup(t int64) (off int64, found bool) {
	n := 0
	i := sort.Search(len(x.entries), func(i int) bool {
		n++
		return x.entries[i].TS >= t
	})
	x.probes.Store(int64(n))
	if i == len(x.entries) {
		return 0, false
	}
	return x.entries[i].Off, true
}

// Entries 返回索引项副本（按追加顺序，TS 与 Off 均严格递增）。
func (x *Index) Entries() []Entry {
	out := make([]Entry, len(x.entries))
	copy(out, x.entries)
	return out
}

// SelfCheck 内部核验：索引严格单调，且对 m 个索引项的任意 Lookup
// 探查数不超过 2*ceil(log2(m+1))+4（即对数而非线性）。只返回结论，
// 不泄露 probes 数值。
func (x *Index) SelfCheck() bool {
	for _, m := range []int{100, 500, 2000, 10000} {
		var ix Index
		for i := 0; i < m; i++ {
			ix.Add(int64(i), int64(2*i)) // 严格递增，索引恰有 m 项
		}
		es := ix.Entries()
		if len(es) != m {
			return false
		}
		for i := 1; i < m; i++ {
			if es[i].TS <= es[i-1].TS || es[i].Off <= es[i-1].Off {
				return false
			}
		}
		bound := 1
		for v := m + 1; v > 1; v = (v + 1) / 2 { // ceil(log2(m+1))
			bound++
		}
		bound = 2*bound + 4
		for _, t := range []int64{-1, 0, int64(m / 2), int64(m - 1), int64(m), int64(m) + 5} {
			ix.Lookup(t)
			if ix.probes.Load() > int64(bound) {
				return false
			}
		}
	}
	return true
}
