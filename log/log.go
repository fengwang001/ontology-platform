// Package log 实现只追加的全局写日志，支持按 lsn 区间取回（供 catch-up）。
package log

// Entry 是一条写：键、值、全局递增的日志序号（从 1 起）。
type Entry struct {
	Key string
	Val string
	LSN int
}

// Log 是 append-only 的写日志。entries[i].LSN == i+1，因此可按 lsn 直接定位。
type Log struct {
	entries []Entry
}

// New 返回空日志。
func New() *Log { return &Log{} }

// Append 追加一条写，返回其 lsn。
func (l *Log) Append(key, val string) int {
	lsn := len(l.entries) + 1
	l.entries = append(l.entries, Entry{Key: key, Val: val, LSN: lsn})
	return lsn
}

// Range 返回 lsn ∈ (from, to] 的写，按 lsn 升序。调用方须保证 0 <= from <= to <= Len()。
// 直接按下标切片定位，不从头扫描。
func (l *Log) Range(from, to int) []Entry {
	out := make([]Entry, to-from)
	copy(out, l.entries[from:to])
	return out
}

// At 返回 lsn 对应的那条写（lsn 从 1 起）。供自检按位置核对。
func (l *Log) At(lsn int) Entry { return l.entries[lsn-1] }

// Len 返回当前最大 lsn（即条目数）。
func (l *Log) Len() int { return len(l.entries) }
