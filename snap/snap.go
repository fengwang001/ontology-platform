// Package snap 提供点时刻快照副本与位点（cursor）比较语义。
package snap

import "sort"

// Entry 是一个键值对。
type Entry struct {
	Key string
	Val int64
}

// Snapshot 是某一时刻固化的、按 Key 字典序排列的全量副本。
// 固化后不再变化，并发读安全。
type Snapshot struct {
	keys []string
	vals []int64
}

// Capture 把 m 的当前内容按 Key 字典序固化成副本（深拷贝，
// 之后对 m 的写不影响副本）。调用方需自行保证与写 m 的互斥。
func Capture(m map[string]int64) *Snapshot {
	s := &Snapshot{
		keys: make([]string, 0, len(m)),
		vals: make([]int64, 0, len(m)),
	}
	for k := range m {
		s.keys = append(s.keys, k)
	}
	sort.Strings(s.keys)
	for _, k := range s.keys {
		s.vals = append(s.vals, m[k])
	}
	return s
}

// Len 返回快照内键值对个数。
func (s *Snapshot) Len() int { return len(s.keys) }

// At 返回按字典序第 i 个键值对。
func (s *Snapshot) At(i int) Entry { return Entry{Key: s.keys[i], Val: s.vals[i]} }

// After 返回第一个严格大于 cursor 的键的下标（== Len() 表示没有），
// probes 记录定位过程中检查的键个数。二分定位，代价 O(log n)。
// cursor == "" 时直接返回 0（不检查任何键）。
func (s *Snapshot) After(cursor string) (lo int, probes int) {
	if cursor == "" {
		return 0, 0
	}
	lo, hi := 0, len(s.keys)
	for lo < hi {
		probes++
		mid := int(uint(lo+hi) >> 1)
		if s.keys[mid] > cursor { // 严格大于：等于位点的键绝不重导
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo, probes
}
