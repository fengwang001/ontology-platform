package ontology

import (
	"encoding/json"
	"io"
	"sync"
)

// CallLogger 记录每次呈现调用的输入、输出与裁决依据。
type CallLogger interface {
	LogCall(entry CallLogEntry)
}

// CallLogEntry 是一次呈现调用的完整日志。
type CallLogEntry struct {
	Subject  string
	Instance Instance
	Result   Result
}

// NopLogger 丢弃日志。
type NopLogger struct{}

func (NopLogger) LogCall(CallLogEntry) {}

// JSONLogger 以 JSON Lines 形式并发安全地写出调用日志。
type JSONLogger struct {
	mu  sync.Mutex
	w   io.Writer
	enc *json.Encoder
}

func NewJSONLogger(w io.Writer) *JSONLogger {
	return &JSONLogger{w: w, enc: json.NewEncoder(w)}
}

func (l *JSONLogger) LogCall(entry CallLogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = l.enc.Encode(entry)
}
