package ontology

import (
	"encoding/json"
	"io"
	"sync"
)

// JSONLogger 把每次调用以 JSON Lines 写出。
type JSONLogger struct {
	mu sync.Mutex
	w  io.Writer
}

// NewJSONLogger 构造 JSONL 日志器。
func NewJSONLogger(w io.Writer) *JSONLogger { return &JSONLogger{w: w} }

// logEntry 完整记录每次调用的输入、据以裁决的传播路径与最终输出。
type logEntry struct {
	Input       ActionDeclaration `json:"input"`
	Committed   bool              `json:"committed"`
	Reason      string            `json:"reason,omitempty"`
	Checks      int               `json:"checks"`
	AuditIndex  int64             `json:"audit_index"`
	Path        []TraversalEntry  `json:"path"`
	Skipped     []SkippedInstance `json:"skipped,omitempty"`
	StateBefore WorldState        `json:"state_before"`
}

func (l *JSONLogger) LogCall(a ActionDeclaration, r Report, before WorldState) {
	if l == nil || l.w == nil {
		return
	}
	entry := logEntry{
		Input: a, Committed: r.Committed, Reason: r.Reason, Checks: r.Checks,
		AuditIndex: r.AuditIndex, Path: r.Path, Skipped: r.Skipped, StateBefore: before,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.w.Write(append(data, '\n'))
}
