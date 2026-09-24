// Package tlog 是只追加日志本体：位点分配、时间戳校验、容量上限、
// 批量追加的原子性，追加时维护 tidx 时间索引，并提供按时间戳查位点。
// 依赖 tidx。
package tlog

import (
	"errors"
	"sync"

	"ontology/tidx"
)

// 可判定的哨兵错误。
var (
	ErrNegativeTS = errors.New("tlog: negative timestamp")
	ErrCapacity   = errors.New("tlog: message capacity exceeded")
)

// Log 是位点从 base 起连续递增、无空洞的只追加日志。
type Log struct {
	mu      sync.RWMutex
	base    int64
	maxMsgs int
	ts      []int64 // 消息时间戳，下标 i 对应位点 base+i
	idx     tidx.Index
}

// New 构造空日志。base 与 maxMsgs 的合法性由调用方（api 包）校验。
func New(base int64, maxMsgs int) *Log {
	return &Log{base: base, maxMsgs: maxMsgs}
}

// Append 批量追加。任一条 TS 为负或追加后总数超限，整批不生效，
// LEO、索引、已有消息全部不变。成功时返回本批第一条消息的位点。
func (l *Log) Append(batch []int64) (first int64, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, ts := range batch {
		if ts < 0 {
			return 0, ErrNegativeTS
		}
	}
	if len(l.ts)+len(batch) > l.maxMsgs {
		return 0, ErrCapacity
	}
	first = l.base + int64(len(l.ts))
	for _, ts := range batch {
		off := l.base + int64(len(l.ts))
		l.ts = append(l.ts, ts)
		l.idx.Add(ts, off)
	}
	return first, nil
}

// Lookup 返回位点最小的、TS >= t 的消息位点；没有则返回 (LEO, false)。
func (l *Log) Lookup(t int64) (off int64, found bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	off, found = l.idx.Lookup(t)
	if !found {
		return l.base + int64(len(l.ts)), false
	}
	return off, true
}

// LEO 是日志结束位点（下一条要写入的位点，空日志时等于 base）。
func (l *Log) LEO() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.base + int64(len(l.ts))
}

// Snapshot 返回全部消息时间戳的副本（下标 i 对应位点 base+i）。
func (l *Log) Snapshot() []int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]int64, len(l.ts))
	copy(out, l.ts)
	return out
}

// Index 返回时间索引项副本。
func (l *Log) Index() []tidx.Entry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.idx.Entries()
}
