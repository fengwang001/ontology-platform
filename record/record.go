package record

import (
	"ontology/interval"
)

// Record 是一条双时间记录：Key/Value 为业务内容，Valid 是有效时间区间，
// Txn 是事务时间区间（End 为零值表示至今）。
type Record struct {
	Key   string
	Value string
	Valid interval.Interval
	Txn   interval.Interval
}

// New 构造记录并校验两条区间：任一空区间都以 interval.ErrEmptyInterval 拒绝。
func New(key, value string, valid, txn interval.Interval) (Record, error) {
	if valid.Empty() || txn.Empty() {
		return Record{}, interval.ErrEmptyInterval
	}
	return Record{Key: key, Value: value, Valid: valid, Txn: txn}, nil
}

// ActiveAt 报告记录在给定的 (有效时刻, 事务时刻) 双时间点上是否生效。
func (r Record) ActiveAt(validAt, txnAt interface {
}) bool {
	return false
}
