// Package audit 对分发出的号流做全局唯一性、单调性与空洞自检。
package audit

import "slices"

// Report 汇总一次自检结果。
type Report struct {
	Total      int    // 号总数
	Unique     bool   // 全局无重复
	Monotonic  bool   // 每条流内部严格递增
	Duplicates int    // 重复号个数
	GapRanges  int    // 空洞区间数
	GapTotal   uint64 // 空洞总长度（缺失号的个数）
}

// Analyze 检查若干条号流（每条来自一个实例）。
// 空洞定义为全局排序后相邻号之间缺失的区间；崩溃丢弃与租约作废
// 都会产生空洞，属于预期行为，但必须可度量。
func Analyze(streams ...[]uint64) Report {
	r := Report{Unique: true, Monotonic: true}
	seen := make(map[uint64]struct{})
	var all []uint64
	for _, s := range streams {
		for i, id := range s {
			if i > 0 && id <= s[i-1] {
				r.Monotonic = false
			}
			if _, dup := seen[id]; dup {
				r.Unique = false
				r.Duplicates++
			}
			seen[id] = struct{}{}
			all = append(all, id)
			r.Total++
		}
	}
	slices.Sort(all)
	for i := 1; i < len(all); i++ {
		if d := all[i] - all[i-1]; d > 1 {
			r.GapRanges++
			r.GapTotal += d - 1
		}
	}
	return r
}
