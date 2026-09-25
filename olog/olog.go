// Package olog 是去重的追加日志 + 物化视图（后写覆盖）+ 已提交 (txID,Seq) 集合。
// 依赖 txn；并发安全，状态只在进程内存。
package olog

import (
	"fmt"
	"sync"

	"ontology/txn"
)

type entry struct {
	id  txn.RecID
	rec txn.Rec
}

// Log 持有追加日志、幂等集合与物化视图。
type Log struct {
	mu    sync.Mutex
	log   []entry
	seen  map[txn.RecID]struct{}
	view  map[string]int64
	scans int // 非导出：最近一次 Commit 幂等判定的哈希探测次数
}

// New 创建空日志。
func New() *Log {
	return &Log{seen: map[txn.RecID]struct{}{}, view: map[string]int64{}}
}

// Commit 先整批校验（失败则什么都不改），再按 (txID,Seq) 判定：
// 已提交的幂等跳过，其余按序追加并后写覆盖视图。返回本次新追加条数。
func (l *Log) Commit(txID string, recs []txn.Rec) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.scans = 0
	if err := txn.Validate(txID, recs); err != nil {
		return 0, err // 失败不留痕：此刻尚未触碰任何状态
	}
	fresh := txn.Fresh(txID, recs, func(id txn.RecID) bool {
		l.scans++ // 每次哈希集合成员判定计一次探测，与已提交总量无关
		_, ok := l.seen[id]
		return ok
	})
	for _, r := range fresh {
		id := txn.ID(txID, r.Seq)
		l.seen[id] = struct{}{}
		l.log = append(l.log, entry{id: id, rec: r})
		l.view[r.Key] = r.Val // 后写覆盖
	}
	return len(fresh), nil
}

// View 返回物化视图的副本，调用方可任意读写，不会读到撕裂状态。
func (l *Log) View() map[string]int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[string]int64, len(l.view))
	for k, v := range l.view {
		out[k] = v
	}
	return out
}

// ReplayScan 返回最近一次 Commit 的幂等判定探测次数（仅包内测试使用）。
func (l *Log) replayScan() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.scans
}

// recomputeLocked 对去重日志按 Key 后写覆盖做批量重算（调用方持锁）。
func (l *Log) recomputeLocked() map[string]int64 {
	out := map[string]int64{}
	for _, e := range l.log { // l.log 本身每个 (txID,Seq) 恰一次
		out[e.rec.Key] = e.rec.Val
	}
	return out
}

// recompute 对去重日志按 Key 后写覆盖做批量重算（同包测试用）。
func (l *Log) recompute() map[string]int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recomputeLocked()
}

// logLen 返回日志条数（同包测试用）。
func (l *Log) logLen() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.log)
}

// CheckReplayLookup 核验性质：重放一条已提交记录时，幂等判定的哈希探测次数
// 不随已提交总量 m 线性增长（被一个与 m 无关的小常数封顶）。
// 它只返回成败，绝不导出计数器的数值。
func CheckReplayLookup() error {
	for _, m := range []int{100, 1000, 10000} {
		l := New()
		recs := make([]txn.Rec, m)
		for i := range recs {
			recs[i] = txn.Rec{Seq: i, Key: fmt.Sprintf("k%d", i), Val: int64(i)}
		}
		if n, err := l.Commit("bulk", recs); err != nil || n != m {
			return fmt.Errorf("olog: scale commit failed at m=%d", m)
		}
		if n, _ := l.Commit("bulk", []txn.Rec{recs[m/2]}); n != 0 {
			return fmt.Errorf("olog: replay not idempotent at m=%d", m)
		}
		l.mu.Lock()
		got := l.scans
		l.mu.Unlock()
		if got > lookupBudget {
			return fmt.Errorf("olog: replay lookup grew with m=%d", m)
		}
	}
	return nil
}

// lookupBudget 是与 m 无关的探测次数上界：重放单条只做一次哈希查找。
const lookupBudget = 8
