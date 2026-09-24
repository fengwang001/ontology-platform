// Package coalesce 把语法区间归一化为互不重叠、互不相邻的半开区间列表。
// 归一化 = 越界裁剪 + 按起点排序（归并排序）+ 线性扫描合并重叠与相邻。
package coalesce

import (
	"fmt"
	"sync/atomic"

	"ontology/rangespec"
)

// Range 是归一化后的半开区间 [Start, End)，满足 0 <= Start < End <= 资源总长。
type Range struct {
	Start int64
	End   int64
}

// UnsatisfiableError 表示区间集合法但不覆盖资源的任何字节（如 bytes=-0、
// 起点越过末尾、a>b 的空区间）。Total 携带资源总长，供调用方生成 416。
type UnsatisfiableError struct {
	Total int64
}

func (e *UnsatisfiableError) Error() string {
	return fmt.Sprintf("coalesce: ranges unsatisfiable, resource length is %d", e.Total)
}

// compares 是非导出的区间比较次数计数器，用于证明归一化为 O(n log n)。
// 用原子操作保证并发 Normalize 时 go test -race 干净。
var compares atomic.Int64

// Compares 返回自上次 ResetCompares 以来的区间比较次数（仅供测试观测）。
func Compares() int64 { return compares.Load() }

// ResetCompares 清零比较计数器（仅供测试观测）。
func ResetCompares() { compares.Store(0) }

// Normalize 把语法区间按资源总长 total 归一化：
// 越界裁剪到 [0,total)，丢弃空区间，排序后合并重叠与相邻区间。
// 全部区间为空时返回 *UnsatisfiableError。
func Normalize(specs []rangespec.Range, total int64) ([]Range, error) {
	var rs []Range
	for _, s := range specs {
		if r, ok := clamp(s, total); ok {
			rs = append(rs, r)
		}
	}
	if len(rs) == 0 {
		return nil, &UnsatisfiableError{Total: total}
	}
	sortRanges(rs)
	return merge(rs), nil
}

// clamp 把一条语法区间裁剪为 [0,total) 内的半开区间；空区间 ok=false。
func clamp(s rangespec.Range, total int64) (Range, bool) {
	if total <= 0 {
		return Range{}, false
	}
	if s.Suffix >= 0 { // -n：末尾 n 字节；n>total 取全部；n==0 为空
		if s.Suffix == 0 {
			return Range{}, false
		}
		start := int64(0)
		if s.Suffix < total {
			start = total - s.Suffix
		}
		return Range{Start: start, End: total}, true
	}
	end := total // a- 或 b 越界：裁到末尾
	if s.End >= 0 && s.End < total-1 {
		end = s.End + 1
	}
	if s.Start >= total || end <= s.Start { // 起点越界或空区间
		return Range{}, false
	}
	return Range{Start: s.Start, End: end}, true
}

// merge 线性扫描已排序区间，合并重叠（next.Start<=cur.End）与相邻
// （next.Start==cur.End）区间，保证输出互不重叠、互不相邻。
func merge(rs []Range) []Range {
	out := rs[:1]
	for _, next := range rs[1:] {
		compares.Add(1)
		cur := &out[len(out)-1]
		if next.Start <= cur.End {
			if next.End > cur.End {
				cur.End = next.End
			}
		} else {
			out = append(out, next)
		}
	}
	return out
}

// sortRanges 自底向上迭代归并排序，按 Start 升序。
// 比较次数上界为 n*ceil(log2 n)，与输入分布无关。
func sortRanges(rs []Range) {
	n := len(rs)
	buf := make([]Range, n)
	for width := 1; width < n; width *= 2 {
		for lo := 0; lo < n; lo += 2 * width {
			mid := min(lo+width, n)
			hi := min(lo+2*width, n)
			i, j, k := lo, mid, lo
			for i < mid && j < hi {
				compares.Add(1)
				if rs[i].Start <= rs[j].Start {
					buf[k] = rs[i]
					i++
				} else {
					buf[k] = rs[j]
					j++
				}
				k++
			}
			for i < mid {
				buf[k] = rs[i]
				i++
				k++
			}
			for j < hi {
				buf[k] = rs[j]
				j++
				k++
			}
		}
		copy(rs, buf)
	}
}
