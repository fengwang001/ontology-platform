// Package asof 提供按 (有效时刻, 事务时刻) 的双时间点查。
// 查询只扫描目标键的版本链，不全表扫描；查询是纯函数。
package asof

import (
	"sync/atomic"

	"ontology/store"
)

// Q 是查询器，checked 记录最近一次查询检查过的记录数。
type Q struct {
	st      *store.Store
	checked atomic.Int64
}

// New 创建查询器。
func New(st *store.Store) *Q {
	return &Q{st: st}
}

// Query 返回 (有效时刻, 事务时刻) 下键的可见值；found 区分「不存在」与零值。
func (q *Q) Query(key string, validAt, txAt int64) (value int64, found bool) {
	recs := q.st.Snapshot(key)
	var n int64
	for _, r := range recs {
		n++
		if r.VisibleAt(validAt, txAt) {
			q.checked.Store(n)
			return r.Value, true
		}
	}
	q.checked.Store(n)
	return 0, false
}

// Checked 返回最近一次查询检查过的记录数。
func (q *Q) Checked() int64 {
	return q.checked.Load()
}
