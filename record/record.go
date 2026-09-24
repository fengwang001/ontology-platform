// Package record 定义双时间记录：键、值、有效区间、事务区间。
package record

import "ontology/interval"

// R 是一条双时间记录，可看作「有效区间 × 事务区间」的矩形。
type R struct {
	Key   string
	Value int64
	Valid interval.I
	Tx    interval.I
}

// New 构造记录，事务区间为 [txStart, Infinity)。
func New(key string, value int64, valid interval.I, txStart int64) R {
	return R{
		Key:   key,
		Value: value,
		Valid: valid,
		Tx:    interval.I{Start: txStart, End: interval.Infinity},
	}
}

// VisibleAt 判定记录是否命中 (有效时刻, 事务时刻) 点查。
func (r R) VisibleAt(validAt, txAt int64) bool {
	return r.Valid.Contains(validAt) && r.Tx.Contains(txAt)
}

// RectDisjoint 判定两条记录的矩形是否互不相交。
func RectDisjoint(a, b R) bool {
	return !a.Valid.Overlaps(b.Valid) || !a.Tx.Overlaps(b.Tx)
}
