// Package snap 提供点时刻快照副本与位点（cursor）比较语义。
// 不依赖其他包。
package snap

import "sort"

// Entry 是一个键值对。
type Entry struct {
	Key string
	Val int64
}

// Snapshot 是某一时刻全部键值对的只读有序副本。
// 键按字典序升序固化，创建后不可变，可安全并发读。
type Snapshot struct {
	keys []string
	vals []int64
}

// NewSnapshot 拷贝 src 的当前内容并按 Key 字典序固化。
// 此后对 src 的修改不影响副本。
func NewSnapshot(src map[string]int64) *Snapshot {
	keys := make([]string, 0, len(src))
	for k := range src {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	vals := make([]int64, len(keys))
	for i, k := range keys {
		vals[i] = src[k]
	}
	return &Snapshot{keys: keys, vals: vals}
}

// Len 返回快照中的键个数。
func (s *Snapshot) Len() int { return len(s.keys) }

// Locate 返回第一个严格大于 cursor 的键的下标（== Len 表示没有）。
// checked 返回定位过程中检查的键个数（二分比较次数），供调用方统计。
// 比较语义是严格 >：cursor 命中的键本身绝不再次导出。
func (s *Snapshot) Locate(cursor string) (idx int, checked int) {
	lo, hi := 0, len(s.keys)
	for lo < hi {
		checked++
		mid := int(uint(lo+hi) >> 1)
		if s.keys[mid] > cursor {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo, checked
}

// Entries 返回从下标 from 开始的至多 n 个键值对。
func (s *Snapshot) Entries(from, n int) []Entry {
	if from >= len(s.keys) || n <= 0 {
		return nil
	}
	end := from + n
	if end > len(s.keys) {
		end = len(s.keys)
	}
	out := make([]Entry, end-from)
	for i := from; i < end; i++ {
		out[i-from] = Entry{Key: s.keys[i], Val: s.vals[i]}
	}
	return out
}

// All 返回快照全量键值对（字典序）。
func (s *Snapshot) All() []Entry { return s.Entries(0, len(s.keys)) }
