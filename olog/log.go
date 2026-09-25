// Package olog 是去重追加日志 + 后写覆盖物化视图 + 已提交 (txID,Seq) 集合。
// 依赖方向：olog -> txn（单向）。
package olog

import (
	"sync"

	"ontology/txn"
)

// Rec 复用 txn.Rec，调用方不需要为同一概念引入两个类型。
type Rec = txn.Rec

type idKey struct {
	txID string
	seq  int
}

type entry struct {
	txID string
	rec  Rec
}

// Log 全部状态都在进程内存；mu 保护下面四个字段。
type Log struct {
	mu        sync.Mutex
	log       []entry
	view      map[string]int64
	done      map[idKey]struct{}
	probeScan int // 最近一次 Commit 幂等判定实际扫描的条目数（非导出）
}

// New 创建空日志。
func New() *Log {
	return &Log{
		view: map[string]int64{},
		done: map[idKey]struct{}{},
	}
}

// Commit 整批校验通过后，按追加序写入未提交的 (txID,Seq)，
// 返回本次新追加条数；校验失败则整体拒绝、日志/视图/已提交集合均不变。
func (l *Log) Commit(txID string, recs []Rec) (int, error) {
	// 第一步：锁外纯校验。失败即返回，此刻从未触碰任何状态（I4）。
	if err := txn.Validate(txID, recs); err != nil {
		return 0, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	// 第二步：哈希集合判定，每次查询只探测一个已提交条目（O(1)，不线性扫描）。
	scan := 0
	isCommitted := func(id string, seq int) bool {
		scan++
		_, ok := l.done[idKey{id, seq}]
		return ok
	}
	fresh := txn.Plan(txID, recs, isCommitted)
	l.probeScan = scan
	// 第三步：校验已保证批内 Seq 唯一，故 fresh 全部是未提交键，
	// 此刻才第一次修改状态：追加日志、登记已提交、后写覆盖视图。
	for _, r := range fresh {
		l.log = append(l.log, entry{txID: txID, rec: r})
		l.done[idKey{txID, r.Seq}] = struct{}{}
		l.view[r.Key] = r.Val
	}
	return len(fresh), nil
}

// View 返回物化视图的拷贝（后写覆盖：同一 Key 以最后落日志者为准）。
// 锁内复制，并发读到的永远是某个一致快照，不会撕裂。
func (l *Log) View() map[string]int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[string]int64, len(l.view))
	for k, v := range l.view {
		out[k] = v
	}
	return out
}
