// Package seq 负责稳定序号分配与每键变更历史（append-only，按 sn 升序）。
// 不依赖其他包。
package seq

// Change 是一条已分配稳定序号的变更。Sn 从 1 起连续递增，到达顺序即序号。
type Change struct {
	Key string
	Ver int64
	Val int64
	Sn  int64
}

// Log 是 append-only 的变更日志：全局序号 + 每键历史。
// 不是并发安全的；并发控制由上层（api）负责。
type Log struct {
	next int64
	hist map[string][]Change
}

// NewLog 返回空日志，下一条变更的 sn 为 1。
func NewLog() *Log {
	return &Log{next: 1, hist: make(map[string][]Change)}
}

// Append 为变更分配下一个稳定序号并追加到该键历史末尾，返回带序号的变更。
func (l *Log) Append(key string, ver, val int64) Change {
	c := Change{Key: key, Ver: ver, Val: val, Sn: l.next}
	l.next++
	l.hist[key] = append(l.hist[key], c)
	return c
}

// Len 返回该键已追加的变更条数。
func (l *Log) Len(key string) int {
	return len(l.hist[key])
}

// History 返回该键全部变更的副本，按 sn 升序（追加顺序即 sn 升序）。
// 返回副本保证调用方无法改动日志内部状态。
func (l *Log) History(key string) []Change {
	src := l.hist[key]
	out := make([]Change, len(src))
	copy(out, src)
	return out
}
