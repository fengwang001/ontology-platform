package ontology

import (
	"encoding/json"
	"io"
	"sync"
)

// LogEntry 记录每次操作的输入、输出与三层条件取值。
type LogEntry struct {
	Seq        int64          `json:"seq"`
	Op         string         `json:"op"`
	Input      map[string]any `json:"input"`
	OK         bool           `json:"ok"`
	Output     map[string]any `json:"output,omitempty"`
	Error      string         `json:"error,omitempty"`
	Conditions Conditions     `json:"conditions"`
}

type OpLogger struct {
	mu      sync.Mutex
	w       io.Writer
	entries []LogEntry
}

func NewOpLogger(w io.Writer) *OpLogger {
	return &OpLogger{w: w}
}

// write 持久化一条操作日志：始终留存于内存；若配置了 io.Writer，
// 再以单行 JSON 写出。logger 为 nil 时调用方应整体跳过日志。
func (l *OpLogger) write(entry LogEntry) {
	l.mu.Lock()
	l.entries = append(l.entries, entry)
	var raw []byte
	if l.w != nil {
		raw, _ = json.Marshal(entry)
		raw = append(raw, '\n')
	}
	l.mu.Unlock()
	if l.w != nil {
		_, _ = l.w.Write(raw)
	}
}

func (l *OpLogger) Entries() []LogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]LogEntry, len(l.entries))
	copy(out, l.entries)
	return out
}

var _ = json.Marshal
