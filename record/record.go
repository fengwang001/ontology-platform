// Package record 定义双时间记录：键、值、有效区间、事务区间。
package record

import "ontology/interval"

// Record 是一条双时间记录。
// Valid 为有效时间区间，Tx 为事务时间区间，均为左闭右开。
type Record struct {
	Key   string
	Value int64
	Valid interval.Interval
	Tx    interval.Interval
}

// Covers 报告记录是否覆盖 (有效时刻, 事务时刻) 这个二维点。
func (r Record) Covers(validAt, txAt int64) bool {
	return r.Valid.Contains(validAt) && r.Tx.Contains(txAt)
}

// Current 报告记录的事务区间是否尚未截止。
func (r Record) Current() bool {
	return r.Tx.End == interval.Forever
}

// Disjoint 报告两条记录的「有效 × 事务」矩形是否互不相交。
func Disjoint(a, b Record) bool {
	return !a.Valid.Overlaps(b.Valid) || !a.Tx.Overlaps(b.Tx)
}
