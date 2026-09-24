// Package log 是追加型热日志：追加、Checkpoint 推进与持久化位点 cp 的维护。
// 不依赖其他包。全部状态在进程内存，Read 与写操作可并发。
package log

import (
	"errors"
	"sync"
)

// ErrCheckpointInvalid：off >= 当前追加位点（越界）或 off < cp（回退）。
var ErrCheckpointInvalid = errors.New("log: checkpoint out of range or regressed")

// Entry 是一条热日志条目，Offset 从 0 起连续。
type Entry struct {
	Offset  uint64
	Payload string
}

// Log 是进程内热日志。cp 为最大的已 Checkpoint off，无 Checkpoint 时为 -1；
// cp 是单个标量字段，截断合法性判定只需读它一个数（见 trunc 包计数器）。
type Log struct {
	mu      sync.RWMutex
	entries []Entry // 仅保留 [first, next) 的热条目
	first   uint64  // f：实际起始 offset
	next    uint64  // 当前追加位点
	cp      int64   // 持久化位点，-1 表示尚无
}

// New 创建空日志。
func New() *Log { return &Log{cp: -1} }

// Append 追加到下一个 offset（从 0 连续）并返回它。
func (l *Log) Append(payload string) (uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	off := l.next
	l.entries = append(l.entries, Entry{Offset: off, Payload: payload})
	l.next++
	return off, nil
}

// Checkpoint 声明 [0, off] 已持久化；要求 off < 追加位点且不回退（相等幂等允许）。
// 非法时不改任何状态。
func (l *Log) Checkpoint(off uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if off >= l.next || int64(off) < l.cp {
		return ErrCheckpointInvalid
	}
	if int64(off) > l.cp {
		l.cp = int64(off)
	}
	return nil
}

// CP 返回持久化位点；无 Checkpoint 时为 -1。
func (l *Log) CP() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.cp
}

// Next 返回当前追加位点（下一个 offset）。
func (l *Log) Next() uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.next
}

// First 返回热日志实际起始 offset f。
func (l *Log) First() uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.first
}

// Read 返回热日志中 offset >= from 的条目副本（按序），可与写并发。
func (l *Log) Read(from uint64) []Entry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]Entry, 0, len(l.entries))
	for _, e := range l.entries {
		if e.Offset >= from {
			out = append(out, e)
		}
	}
	return out
}

// DeleteBefore 是截断的物理原语：移除 offset < k 的热条目并把 f 抬到 k。
// 调用方（trunc）必须保证 k 合法且标记已先落盘。
func (l *Log) DeleteBefore(k uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if k <= l.first {
		return
	}
	drop := 0
	for drop < len(l.entries) && l.entries[drop].Offset < k {
		drop++
	}
	l.entries = append(l.entries[:0:0], l.entries[drop:]...)
	l.first = k
}
