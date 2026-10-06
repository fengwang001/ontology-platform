package traffic

// Logger 记录每次操作的输入、输出与判定依据。
type Logger interface {
	Log(entry LogEntry)
}

// LogEntry 是一次操作的结构化记录。
type LogEntry struct {
	Time   float64
	Op     string
	Input  string
	Output string
	Reason string
}

type discardLogger struct{}

func (discardLogger) Log(LogEntry) {}

// NewSliceLogger 把日志收集到内存切片，便于测试断言。
func NewSliceLogger() *SliceLogger { return &SliceLogger{} }

// SliceLogger 见 NewSliceLogger。
type SliceLogger struct{ Entries []LogEntry }

// Log 实现 Logger。
func (s *SliceLogger) Log(e LogEntry) { s.Entries = append(s.Entries, e) }
