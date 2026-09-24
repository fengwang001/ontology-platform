// Package coalesce 把 rangespec 解析出的区间按资源总长归一化：
// 越界裁剪、按起点排序、重叠与相邻合并。算法为 O(n log n)：
// 逐条解析裁剪 O(n) + 排序 O(n log n) + 单趟合并扫描 O(n)，
// 不做任何两两配对比较。包内非导出计数器记录区间比较次数，
// 供测试与演示程序验证复杂度上界。
package coalesce

import (
	"sort"
	"sync/atomic"

	"ontology/rangespec"
)

// Range 是归一化后的闭区间 [Start, End]，满足 0 <= Start <= End < total。
type Range struct {
	Start int64
	End   int64
}

// compareCount 记录排序与合并过程中的区间比较次数（非导出，原子读写，
// 保证多 goroutine 并发归一化时 -race 干净）。
var compareCount atomic.Int64

// CompareCount 返回累计区间比较次数。
func CompareCount() int64 { return compareCount.Load() }

// ResetCompareCount 清零比较计数器。
func ResetCompareCount() { compareCount.Store(0) }

// Normalize 把 specs 按资源总长 total 归一化为互不重叠、互不相邻、
// 按起点升序的闭区间列表。越界裁剪而非报错；全部区间都取不到字节时
// 返回 *rangespec.UnsatisfiableError（携带 total）。
func Normalize(specs []rangespec.Spec, total int64) ([]Range, error) {
	rs := make([]Range, 0, len(specs))
	for _, s := range specs {
		if r, ok := resolve(s, total); ok {
			rs = append(rs, r)
		}
	}
	if len(rs) == 0 {
		return nil, &rangespec.UnsatisfiableError{Total: total}
	}
	sort.Slice(rs, func(i, j int) bool {
		compareCount.Add(1)
		return rs[i].Start < rs[j].Start
	})
	merged := make([]Range, 0, len(rs))
	cur := rs[0]
	for _, r := range rs[1:] {
		compareCount.Add(1)
		if r.Start <= cur.End+1 { // 重叠或相邻（首尾相接）都合并
			if r.End > cur.End {
				cur.End = r.End
			}
		} else {
			merged = append(merged, cur)
			cur = r
		}
	}
	return append(merged, cur), nil
}

// resolve 把单条 Spec 解析成具体闭区间；ok==false 表示该区间不可满足。
func resolve(s rangespec.Spec, total int64) (Range, bool) {
	if total <= 0 {
		return Range{}, false
	}
	if s.Start < 0 { // 后缀写法：末尾 Suffix 个字节
		if s.Suffix <= 0 { // "-0"：空字节集合，不可满足
			return Range{}, false
		}
		start := total - s.Suffix // n 大于总长时取全部
		if start < 0 {
			start = 0
		}
		return Range{Start: start, End: total - 1}, true
	}
	if s.Start >= total { // 起点越过末尾：不可满足
		return Range{}, false
	}
	end := s.End
	if end < 0 || end >= total { // 开放写法或终点越界：裁到末尾
		end = total - 1
	}
	if end < s.Start { // b < a：取不到字节
		return Range{}, false
	}
	return Range{Start: s.Start, End: end}, true
}
