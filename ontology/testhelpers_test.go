package ontology

import "sync"

// MemoryLogger 收集遍历日志，供测试核对调用方权限、输入与判定依据。
type MemoryLogger struct {
	mu      sync.Mutex
	Entries []LogEntry
}

func (m *MemoryLogger) Log(e LogEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Entries = append(m.Entries, e)
}

func (m *MemoryLogger) Snapshot() []LogEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]LogEntry, len(m.Entries))
	copy(out, m.Entries)
	return out
}

func labelSet(labels ...string) map[string]struct{} {
	s := map[string]struct{}{}
	for _, l := range labels {
		s[l] = struct{}{}
	}
	return s
}

func link(id, from, to, label string) Link {
	return Link{ID: id, From: from, To: to, Label: label}
}
