// Package reconcile 以线性一遍归并比对清单与存储实际内容，
// 输出已写入/缺口/多余三段差异并区分在途与已提交。
package reconcile

import (
	"ontology/batch"
	"ontology/progress"
	"ontology/store"
)

// Interval 是按下标计的半开区间 [Start, End)。
type Interval struct {
	Start int
	End   int
}

// Report 是对账结论。
type Report struct {
	BatchID          string
	CommittedCount   int        // 已提交记录数
	InflightCount    int        // 在途记录数（不计入已提交）
	Written          []Interval // 已写入区间（含在途）
	Missing          []Interval // 缺口区间
	Extra            []string   // 多余记录（存储有、清单无）
	Committed        bool       // 批次是否已提交
	StoreAccesses    int        // 存储/清单访问总次数（上界 3n）
}

// Reconcile 对清单与存储做一遍归并对账。
func Reconcile(st *store.Store, src batch.Source, pro *progress.Progress) (*Report, error) {
	return nil, nil
}
