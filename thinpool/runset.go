package thinpool

import "sort"

// runset 是单个卷虚拟块映射的有序、合并、不相交区间集合（左闭右开）。
// 它只记录“哪些虚拟块已映射”；物理块由池统一计数，因为各卷不共享块。
type runset struct {
	runs  []interval
	total int
}

type interval struct {
	start int
	end   int
}

func newRunset() *runset { return &runset{} }

// contains 报告虚拟块 v 是否已映射。
func (r *runset) contains(v int) bool {
	i := sort.Search(len(r.runs), func(i int) bool { return r.runs[i].end > v })
	return i < len(r.runs) && r.runs[i].start <= v
}

// add 映射虚拟块 v；返回 true 表示新映射，false 表示此前已映射。
func (r *runset) add(v int) bool {
	i := sort.Search(len(r.runs), func(i int) bool { return r.runs[i].end > v })
	if i < len(r.runs) && r.runs[i].start <= v {
		return false
	}
	// 与左邻接区间合并。
	if i > 0 && r.runs[i-1].end == v {
		r.runs[i-1].end = v + 1
		// 再与右邻接区间合并。
		if i < len(r.runs) && r.runs[i].start == v+1 {
			r.runs[i-1].end = r.runs[i].end
			r.runs = append(r.runs[:i], r.runs[i+1:]...)
		}
		r.total++
		return true
	}
	// 仅与右邻接区间合并。
	if i < len(r.runs) && r.runs[i].start == v+1 {
		r.runs[i].start = v
		r.total++
		return true
	}
	// 插入孤立点区间。
	r.runs = append(r.runs, interval{})
	copy(r.runs[i+1:], r.runs[i:])
	r.runs[i] = interval{v, v + 1}
	r.total++
	return true
}

// removeRange 释放 [start,start+length) 内已映射块；
// 返回被释放（此前已映射）的块数。调用方须先完成边界校验。
//
// 复杂度只与“和回收范围相交的已映射区间数”成比例（二分定位 + 局部改写），
// 不遍历范围内的未映射虚拟块。
func (r *runset) removeRange(start, length int) int {
	if length <= 0 {
		return 0
	}
	end := start + length
	i := sort.Search(len(r.runs), func(i int) bool { return r.runs[i].end > start })
	freed := 0
	for i < len(r.runs) && r.runs[i].start < end {
		a := r.runs[i]
		overlapStart := maxInt(a.start, start)
		overlapEnd := minInt(a.end, end)
		freed += overlapEnd - overlapStart
		r.total -= overlapEnd - overlapStart
		switch {
		case a.start < overlapStart && overlapEnd < a.end:
			// 回收区间落在一个已映射区间内部：分裂为左右两段。
			r.runs[i] = interval{a.start, overlapStart}
			r.runs = append(r.runs, interval{})
			copy(r.runs[i+2:], r.runs[i+1:])
			r.runs[i+1] = interval{overlapEnd, a.end}
			return freed
		case a.start < overlapStart:
			// 仅保留左侧残余。
			r.runs[i].end = overlapStart
			i++
		case overlapEnd < a.end:
			// 仅保留右侧残余。
			r.runs[i].start = overlapEnd
			i++
		default:
			// 整个区间被释放。
			r.runs = append(r.runs[:i], r.runs[i+1:]...)
		}
	}
	return freed
}

// count 返回已映射块数。
func (r *runset) count() int { return r.total }

// countIn 返回 [start,end) 内已映射块数。
func (r *runset) countIn(start, end int) int {
	if end <= start {
		return 0
	}
	i := sort.Search(len(r.runs), func(i int) bool { return r.runs[i].end > start })
	n := 0
	for i < len(r.runs) && r.runs[i].start < end {
		n += minInt(r.runs[i].end, end) - maxInt(r.runs[i].start, start)
		i++
	}
	return n
}

// snapshot 返回映射区间的拷贝。
func (r *runset) snapshot() []interval {
	out := make([]interval, len(r.runs))
	copy(out, r.runs)
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
