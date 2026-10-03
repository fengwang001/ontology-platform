// Package merge 把边界不同的同名直方图无损合并到公共边界（边界交集）上。
package merge

import (
	"math"
	"sync/atomic"

	"ontology/schema"
)

// mergeCompares 记录最近一次 Merge 的边界比较次数（非导出，仅供本包测试断言）。
var mergeCompares atomic.Int64

// Merge 把 a、b 合并到两边边界的交集上。
// 错误顺序：参数非法、名字不同、无公共边界、溢出。输入保持不变。
func Merge(a, b *schema.Hist) (schema.Hist, error) {
	if err := a.Validate(); err != nil {
		return schema.Hist{}, err
	}
	if err := b.Validate(); err != nil {
		return schema.Hist{}, err
	}
	if a.Name != b.Name {
		return schema.Hist{}, schema.ErrNameMismatch
	}
	// 单趟归并：同时求边界交集与两侧桶到结果桶的映射。
	// 每轮恰好比较一次并至少推进一个指针，比较次数 <= len(A)+len(B)。
	A, B := a.Bounds, b.Bounds
	var bounds []int64
	var counts []uint64
	var pendA, pendB uint64 // 尚未落入任何公共边界的源桶计数（上移并桶）
	i, j, compares := 0, 0, 0
	for i < len(A) && j < len(B) {
		compares++
		switch {
		case A[i] == B[j]:
			bounds = append(bounds, A[i])
			counts = append(counts, pendA+uint64(a.Counts[i])+pendB+uint64(b.Counts[j]))
			pendA, pendB = 0, 0
			i++
			j++
		case A[i] < B[j]:
			pendA += uint64(a.Counts[i])
			i++
		default:
			pendB += uint64(b.Counts[j])
			j++
		}
	}
	mergeCompares.Store(int64(compares))
	if len(bounds) == 0 {
		return schema.Hist{}, schema.ErrIncompatible
	}
	// 剩余源桶（含两侧溢出桶与未落桶的挂起计数）全部并入结果溢出桶。
	overflow := pendA + pendB
	for ; i < len(A); i++ {
		overflow += uint64(a.Counts[i])
	}
	for ; j < len(B); j++ {
		overflow += uint64(b.Counts[j])
	}
	overflow += uint64(a.Counts[len(A)]) + uint64(b.Counts[len(B)])
	counts = append(counts, overflow)
	sum := uint64(a.Sum) + uint64(b.Sum)
	out := schema.Hist{Name: a.Name, Bounds: bounds, Counts: make([]int64, len(counts))}
	for k, c := range counts {
		if c > math.MaxInt64 {
			return schema.Hist{}, schema.ErrOverflow
		}
		out.Counts[k] = int64(c)
	}
	if sum > math.MaxInt64 {
		return schema.Hist{}, schema.ErrOverflow
	}
	out.Sum = int64(sum)
	return out, nil
}

// MergeAll 对一个或多个同名直方图取全体边界交集后相加，原子且与顺序无关。
// 错误顺序同 Merge：参数非法、名字不同、不兼容、溢出。
func MergeAll(hs []schema.Hist) (schema.Hist, error) {
	if len(hs) == 0 {
		return schema.Hist{}, schema.ErrInvalid
	}
	for k := range hs {
		if err := hs[k].Validate(); err != nil {
			return schema.Hist{}, err
		}
		if hs[k].Name != hs[0].Name {
			return schema.Hist{}, schema.ErrNameMismatch
		}
	}
	common := append([]int64(nil), hs[0].Bounds...)
	for k := 1; k < len(hs); k++ {
		common = intersect(common, hs[k].Bounds)
	}
	if len(common) == 0 {
		return schema.Hist{}, schema.ErrIncompatible
	}
	counts := make([]uint64, len(common)+1)
	var sum uint64
	for k := range hs {
		addOnto(&hs[k], common, counts)
		sum += uint64(hs[k].Sum)
	}
	out := schema.Hist{Name: hs[0].Name, Bounds: common, Counts: make([]int64, len(counts))}
	for k, c := range counts {
		if c > math.MaxInt64 {
			return schema.Hist{}, schema.ErrOverflow
		}
		out.Counts[k] = int64(c)
	}
	if sum > math.MaxInt64 {
		return schema.Hist{}, schema.ErrOverflow
	}
	out.Sum = int64(sum)
	return out, nil
}

// intersect 返回两个严格递增边界 slices 的交集（归并法）。
func intersect(a, b []int64) []int64 {
	var out []int64
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, a[i])
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return out
}

// addOnto 把 h 的各桶计数累加到以 common 为边界的目标计数数组上：
// 每个源桶并入「不小于该桶上界的最小公共边界」所在的桶，没有则入溢出桶。
func addOnto(h *schema.Hist, common []int64, out []uint64) {
	p := 0
	for i, bound := range h.Bounds {
		for p < len(common) && common[p] < bound {
			p++
		}
		out[p] += uint64(h.Counts[i]) // p == len(common) 时即溢出桶
	}
	out[len(common)] += uint64(h.Counts[len(h.Bounds)])
}
