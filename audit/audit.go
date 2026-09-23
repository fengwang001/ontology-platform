// Package audit 对分配结果做全局自检：唯一性、单调性与空洞统计。
package audit

import "sort"

// Report 是自检结果。
type Report struct {
	Total      int    // 分发的号总数
	Duplicates int    // 重复出现的号数（超出首次的部分）
	Monotonic  bool   // 每个实例内是否严格递增
	Gaps       int    // 空洞区间个数
	GapLength  uint64 // 空洞总长度（缺失号的个数）
}

// Analyze 检查若干实例各自分发的号流：跨实例唯一、实例内单调，
// 并在全局排序后统计空洞（空洞是允许的，但必须可度量）。
func Analyze(streams ...[]uint64) Report {
	var r Report
	r.Monotonic = true
	all := make([]uint64, 0)
	for _, s := range streams {
		for i, v := range s {
			if i > 0 && v <= s[i-1] {
				r.Monotonic = false
			}
			all = append(all, v)
		}
	}
	r.Total = len(all)
	if len(all) == 0 {
		return r
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	for i := 1; i < len(all); i++ {
		switch d := all[i] - all[i-1]; {
		case d == 0:
			r.Duplicates++
		case d > 1:
			r.Gaps++
			r.GapLength += d - 1
		}
	}
	return r
}
