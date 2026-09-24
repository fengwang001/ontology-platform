// Package wal 实现源表与变更日志：追加并分配连续 LSN、维护源表当前状态、
// 按键范围读取、按 LSN 区间取条目。不依赖其他包。
package wal

import "sync"

// Op 是日志条目类型，只有 Upsert 与 Delete 两种。
type Op int

const (
	Upsert Op = iota // 把键设为 Val，键不存在则新建
	Delete           // 删除该键；键不存在时源表无变化，但仍写日志
)

// Entry 是一条变更日志条目。LSN 不在结构内：追加时按顺序分配（从 1 开始），
// 即 entries 切片下标 +1。
type Entry struct {
	Op  Op
	Key int64
	Val string
}

// Log 是源表与变更日志，各方法各自加锁，可并发调用。
type Log struct {
	mu      sync.RWMutex
	table   map[int64]string
	entries []Entry // entries[i] 的 LSN 为 i+1
}

func New() *Log { return &Log{table: map[int64]string{}} }

// Append 追加一条条目，分配 LSN 并应用到源表，返回分配的 LSN。
func (l *Log) Append(e Entry) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, e)
	if e.Op == Upsert {
		l.table[e.Key] = e.Val
	} else {
		delete(l.table, e.Key)
	}
	return int64(len(l.entries))
}

// Pos 返回日志当前位置（最后一条的 LSN，空日志为 0）。
func (l *Log) Pos() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return int64(len(l.entries))
}

// Between 返回 LSN 落在 (a, b] 的条目副本；返回切片第 i 条的 LSN 为 a+1+i。
// LSN 连续，直接按下标定位，不扫描。
func (l *Log) Between(a, b int64) []Entry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if n := int64(len(l.entries)); b > n {
		b = n
	}
	if a < 0 {
		a = 0
	}
	if a >= b {
		return nil
	}
	out := make([]Entry, b-a)
	copy(out, l.entries[a:b])
	return out
}

// Range 返回此刻源表中键落在左闭右开 [lo, hi) 的全部行的副本。
func (l *Log) Range(lo, hi int64) map[int64]string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := map[int64]string{}
	for k, v := range l.table {
		if k >= lo && k < hi {
			out[k] = v
		}
	}
	return out
}

// Table 返回源表当前完整状态的副本。
func (l *Log) Table() map[int64]string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make(map[int64]string, len(l.table))
	for k, v := range l.table {
		out[k] = v
	}
	return out
}
