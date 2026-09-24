// Package read 实现双读合并：快照基 + 增量按序应用。依赖 snap。
package read

import (
	"errors"
	"sync/atomic"

	"ontology/snap"
)

// ErrBeforeSnap 表示 atSeq 早于最近快照，读取被拒绝。
var ErrBeforeSnap = errors.New("read: atSeq before snapshot")

// lastScan 记录最近一次 Merge 从日志里顺序扫描过的条数（非导出，不进公开接口）。
var lastScan atomic.Int64

// Merge 以 base（Seq<=snapSeq 的快照）为基，按 Seq 递增应用 incr 中每条日志
// （Put 覆盖、Del 删除），返回 key 的最终值。快照命中走 map，不计入扫描。
func Merge(base map[string]string, incr []snap.Entry, key string) (string, bool) {
	val, ok := base[key]
	n := 0
	for _, e := range incr {
		n++
		if e.Key != key {
			continue
		}
		if e.Op == snap.Put {
			val, ok = e.Val, true
		} else {
			val, ok = "", false
		}
	}
	lastScan.Store(int64(n))
	return val, ok
}

// ReadAt 读取 key 在 atSeq 时刻的值。边界：快照覆盖 [1,snapSeq]，
// 增量覆盖 (snapSeq,atSeq]——含 atSeq、不含 snapSeq，无交无缝。
// entries[i].Seq == i+1，故增量可按下标 O(1) 切片，无需从头扫描。
func ReadAt(entries []snap.Entry, base map[string]string, snapSeq, atSeq int64, key string) (string, bool, error) {
	if atSeq < snapSeq {
		return "", false, ErrBeforeSnap
	}
	end := atSeq
	if n := int64(len(entries)); end > n {
		end = n
	}
	incr := entries[snapSeq:end]
	val, ok := Merge(base, incr, key)
	return val, ok, nil
}
