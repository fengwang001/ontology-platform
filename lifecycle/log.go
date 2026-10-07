package lifecycle

// Logger 记录每次操作的输入/输出与判定依据。
type Logger interface {
	Log(e LogEntry)
}

// SliceLogger 把日志保存在内存切片中，供测试核对。
type SliceLogger struct {
	entries []LogEntry
}

func NewSliceLogger() *SliceLogger { return &SliceLogger{} }

func (l *SliceLogger) Log(e LogEntry) { l.entries = append(l.entries, e) }

func (l *SliceLogger) Entries() []LogEntry {
	out := make([]LogEntry, len(l.entries))
	copy(out, l.entries)
	return out
}
