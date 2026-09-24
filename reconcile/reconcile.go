// Package reconcile 线性对账：比对批次清单与存储实际内容，
// 输出已写入区间、缺口区间与多余记录三段结论，并区分在途与已提交。
package reconcile

import (
	"ontology/batch"
	"ontology/store"
)

// Report 是对账结论。Written/Gaps 精确到区间端点；Extra 为存储多出
// 的键；Committed 与 InFlight 互斥（取决于批次是否已 Commit）。
type Report struct {
	Committed int              // 已提交记录数（批次已 Commit）
	InFlight  int              // 在途记录数（批次未 Commit，不计入已提交）
	Written   []batch.Interval // 已写入区间（清单下标）
	Gaps      []batch.Interval // 缺口区间（清单下标）
	Extra     []string         // 多余记录（存储有而清单无的键）
}

// Reconcile 一遍扫清单加一次取键集，存储访问不超过 n+1 次，整体 O(n)。
// committed 表示批次是否已有提交标记（由调用方从导入目录判定）。
func Reconcile(st *store.Store, b *batch.Batch, committed bool) *Report {
	n := len(b.Records)
	present := make([]bool, n)
	keys := make(map[string]struct{}, n)
	presentCount := 0
	for i, r := range b.Records {
		keys[r.Key] = struct{}{}
		if st.Has(r.Key) {
			present[i] = true
			presentCount++
		}
	}
	rep := &Report{Written: intervals(present, true), Gaps: intervals(present, false)}
	for _, k := range st.Keys() {
		if _, ok := keys[k]; !ok {
			rep.Extra = append(rep.Extra, k)
		}
	}
	if committed {
		rep.Committed = presentCount
	} else {
		rep.InFlight = presentCount
	}
	return rep
}

// intervals 把 present 布尔序列压缩为连续区间；want 为 true 取已写入段，
// 为 false 取缺口段。
func intervals(present []bool, want bool) []batch.Interval {
	var out []batch.Interval
	start := -1
	for i, p := range present {
		if p == want && start < 0 {
			start = i
		}
		if p != want && start >= 0 {
			out = append(out, batch.Interval{Start: start, End: i})
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, batch.Interval{Start: start, End: len(present)})
	}
	return out
}
