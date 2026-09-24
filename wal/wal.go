// Package wal 是源表与变更日志：追加并分配 LSN、维护源表当前状态、
// 按键范围读取快照、按 LSN 区间取条目。它不依赖其他包。
package wal

import "sync"

// Op 只有两种：Upsert 写入/覆盖，Delete 删除（键不存在时源表无变化，仍写日志）。
type Op int

const (
	Upsert Op = iota + 1
	Delete
)

// Entry 是一条变更日志。条目按追加顺序获得连续递增的 LSN（从 1 开始）。
type Entry struct {
	Op  Op
	Key int64
	Val string
}

// Log 同时保存源表当前状态与完整追加日志。所有方法可被并发调用。
type Log struct {
	mu      sync.Mutex
	table   map[int64]string
	entries []Entry // entries[i] 的 LSN = i+1
}

// New 创建空源表与空日志。
func New() *Log {
	return &Log{table: map[int64]string{}}
}

// Append 依次应用并追加一批条目，返回追加后的日志位置（最后一条的 LSN）。
// Upsert 设值（不存在则新建）；Delete 移除键（不存在时表无变化，仍记录日志）。
func (l *Log) Append(es []Entry) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range es {
		switch e.Op {
		case Upsert:
			l.table[e.Key] = e.Val
		case Delete:
			delete(l.table, e.Key)
		}
		l.entries = append(l.entries, e)
	}
	return len(l.entries)
}

// Position 返回当前日志位置；空日志为 0。
func (l *Log) Position() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}

// Snapshot 返回此刻源表中键落在左闭右开 [lo,hi) 的全部行（副本）。
func (l *Log) Snapshot(lo, hi int64) map[int64]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[int64]string)
	for k, v := range l.table {
		if k >= lo && k < hi {
			out[k] = v
		}
	}
	return out
}

// Slice 返回 after < LSN <= to 的条目，按 LSN 顺序。按 LSN 直接下标定位，
// 不从头扫描；调用方必须保证 0 <= after <= to <= Position()。
func (l *Log) Slice(after, to int) []Entry {
	l.mu.Lock()
	defer l.mu.Unlock()
	if to <= after {
		return nil
	}
	out := make([]Entry, to-after)
	copy(out, l.entries[after:to])
	return out
}
