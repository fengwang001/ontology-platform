package ontology

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"sync"
)

// DecisionLogger 记录每次判定的输入、输出与依据。
type DecisionLogger interface {
	LogDecision(ctx context.Context, rec DecisionLogRecord)
}

// DecisionLogRecord 是单次属性级判定日志。
type DecisionLogRecord struct {
	ExportID      string `json:"export_id"`
	Seq           int64  `json:"seq"`
	Subject       string `json:"subject"`
	TypeID        string `json:"type_id"`
	Attribute     string `json:"attribute"`
	Decision      string `json:"decision"`
	HitVersionID  string `json:"hit_version_id"`
	HitRuleID     string `json:"hit_rule_id"`
	DeclaringType string `json:"declaring_type"`
	Effect        string `json:"effect"`
}

type nopLogger struct{}

func (nopLogger) LogDecision(context.Context, DecisionLogRecord) {}

type writerLogger struct {
	mu sync.Mutex
	w  io.Writer
}

// NewJSONLogger 以每行一条 JSON 的方式输出判定日志。w 为 nil 时输出到 stderr。
func NewJSONLogger(w io.Writer) DecisionLogger {
	if w == nil {
		w = os.Stderr
	}
	return &writerLogger{w: w}
}

func (l *writerLogger) LogDecision(_ context.Context, rec DecisionLogRecord) {
	payload, err := json.Marshal(rec)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.w.Write(append(payload, '\n'))
}

// sliceLogger 把判定日志保存在内存切片中，供测试断言。
type sliceLogger struct {
	mu     sync.Mutex
	record []DecisionLogRecord
}

func newSliceLogger() *sliceLogger { return &sliceLogger{} }

func (l *sliceLogger) LogDecision(_ context.Context, rec DecisionLogRecord) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.record = append(l.record, rec)
}

func (l *sliceLogger) snapshot() []DecisionLogRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]DecisionLogRecord, len(l.record))
	copy(out, l.record)
	return out
}
