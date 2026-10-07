package authz

import "sync"

// LogEntry 完整记录一次调用的输入、最终输出以及据以裁决的
// 生效时序依据。日志追加顺序即并发调用的串行化顺序。
type LogEntry struct {
	Seq    int
	Call   string // "submit" / "withdraw" / "decide" / "advance"
	Input  string
	Output string
	Basis  []ChangeID
	Err    error
}

// Logger 接收每次调用的日志条目。
type Logger interface {
	Log(LogEntry)
}

// MemLogger 为内存日志实现，供测试与审计使用。
type MemLogger struct {
	mu      sync.Mutex
	entries []LogEntry
}

// Log 追加一条日志。
func (m *MemLogger) Log(e LogEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, e)
}

// Entries 返回全部日志的副本。
func (m *MemLogger) Entries() []LogEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]LogEntry, len(m.entries))
	copy(out, m.entries)
	return out
}
