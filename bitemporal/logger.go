package bitemporal

import "sync"

// Logger 记录每次查询的输入参数、返回路径集合与每条路径的三方记录标识。
type Logger interface {
	Log(entry QueryLogEntry)
}

// QueryLogEntry 是一条查询日志。
type QueryLogEntry struct {
	Query  Query
	Result *Result
	Err    error
}

// MemoryLogger 是并发安全的内存日志，主要用于测试与本地核对。
type MemoryLogger struct {
	mu      sync.Mutex
	entries []QueryLogEntry
}

// NewMemoryLogger 创建内存日志。
func NewMemoryLogger() *MemoryLogger {
	return &MemoryLogger{}
}

// Log 追加一条查询日志。
func (l *MemoryLogger) Log(entry QueryLogEntry) {
	l.mu.Lock()
	l.entries = append(l.entries, entry)
	l.mu.Unlock()
}

// Entries 返回日志副本。
func (l *MemoryLogger) Entries() []QueryLogEntry {
	l.mu.Lock()
	out := make([]QueryLogEntry, len(l.entries))
	copy(out, l.entries)
	l.mu.Unlock()
	return out
}
