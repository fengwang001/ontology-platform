// Package asof 按 (有效时刻, 事务时刻) 查询双时间记录。
// 查询只遍历目标键的版本链，不做全表扫描。
package asof

import (
	"sync/atomic"

	"ontology/record"
	"ontology/store"
)

// checked 记录最近一次 Query 检查过的记录数（非导出计数器）。
var checked atomic.Int64

// Checked 返回最近一次 Query 检查过的记录数。
func Checked() int64 { return checked.Load() }

// Query 返回在事务时刻 txAt 所知的、于有效时刻 validAt 成立的记录。
// 第二个返回值区分「不存在」与「值为零值」。
func Query(s *store.Store, key string, validAt, txAt int64) (record.Record, bool) {
	checked.Store(0)
	for _, r := range s.Versions(key) {
		checked.Add(1)
		if r.Covers(validAt, txAt) {
			return r, true
		}
	}
	return record.Record{}, false
}
