// Package export 在点时刻快照上做分块导出：Next/Resume、done 判定、位点前进。
// 依赖 snap。
package export

import (
	"errors"
	"sync"

	"ontology/snap"
)

// ErrFinished：快照已导出完毕（done = true 之后）再调 Next/Resume。
var ErrFinished = errors.New("export: snapshot already finished")

// Exporter 从一个不可变快照分块导出。并发安全。
type Exporter struct {
	snap *snap.Snapshot
	size int

	mu       sync.Mutex
	finished bool
	checked  int // 最近一次 Next 为定位位点后第一个键而检查的键个数（非导出，不外泄）
}

// NewExporter 在快照 s 上建导出器，每块至多 size 个键值对。
func NewExporter(s *snap.Snapshot, size int) *Exporter {
	return &Exporter{snap: s, size: size}
}

// Next 返回所有 key > cursor（严格大于）中字典序最小的至多 size 个键值对。
// newCursor 为最后导出的键（空块为 ""）。本块导出后位点再无剩余键时 done = true
// （末块恰好满块也是 true，不需要额外空块）。done 之后再调返回 ErrFinished。
func (e *Exporter) Next(cursor string) (entries []snap.Entry, newCursor string, done bool, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.finished {
		return nil, "", false, ErrFinished
	}
	idx, c := e.snap.Locate(cursor)
	e.checked = c
	entries = e.snap.Entries(idx, e.size)
	if len(entries) > 0 {
		newCursor = entries[len(entries)-1].Key
	}
	done = idx+len(entries) >= e.snap.Len() // 位点后已无剩余键
	if done {
		e.finished = true
	}
	return entries, newCursor, done, nil
}

// Resume 等价于 Next：把上次的 newCursor 传回来即可接着导，位点键本身绝不重复导出。
func (e *Exporter) Resume(cursor string) ([]snap.Entry, string, bool, error) {
	return e.Next(cursor)
}
