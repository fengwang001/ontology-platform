// Package reconcile 对账：线性一遍比对清单与存储实际内容，输出三段差异。
package reconcile

import (
	"ontology/batch"
	"ontology/store"
)

// Report 是对账结果：已写入区间、缺口区间（均精确到端点）、多余记录，
// 以及按批次是否已 Commit 区分的已提交/在途统计。
type Report struct {
	Written   [][2]int // 已写入区间（清单下标，半开）
	Gaps      [][2]int // 缺口区间（清单下标，半开）
	Extra     []string // 存储有而清单无的多余记录
	Committed int      // 已提交记录数（批次已 Commit）
	InTransit int      // 在途记录数（批次未 Commit）
}

// Reconcile 一趟扫清单（n 次 Has）+ 一趟扫存储（≤n+extra 次访问），
// 总访问 ≤ 3n，无 O(n²)。committed 为批次是否已 Commit；
// 未 Commit 批次的已写记录计入在途，不计入已提交。
func Reconcile(m batch.Manifest, st *store.Store, committed bool) Report {
	var r Report
	n := len(m.Keys)
	present, start, cur, started := 0, 0, false, false
	emit := func(end int) {
		if cur {
			r.Written = append(r.Written, [2]int{start, end})
		} else {
			r.Gaps = append(r.Gaps, [2]int{start, end})
		}
	}
	set := make(map[string]struct{}, n)
	for i, k := range m.Keys {
		set[k] = struct{}{}
		has := st.Has(k)
		if has {
			present++
		}
		switch {
		case !started:
			start, cur, started = i, has, true
		case has != cur:
			emit(i)
			start, cur = i, has
		}
	}
	if started {
		emit(n)
	}
	for _, k := range st.Keys() {
		if _, ok := set[k]; !ok {
			r.Extra = append(r.Extra, k)
		}
	}
	if committed {
		r.Committed = present
	} else {
		r.InTransit = present
	}
	return r
}
