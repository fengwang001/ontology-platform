package delegation

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

// LogEntry 记录一次 API 调用的完整审计信息：
// 输入、最终输出、错误以及据以裁决的委托链依据。
type LogEntry struct {
	Time    time.Time      `json:"time"`
	Op      string         `json:"op"`
	Input   any            `json:"input,omitempty"`
	Output  any            `json:"output,omitempty"`
	Error   string         `json:"error,omitempty"`
	Witness []DelegationID `json:"witness,omitempty"`
}

// Logger 接收每次调用的审计日志。
type Logger interface {
	Log(entry LogEntry)
}

// JSONLogger 将审计日志以 JSON Lines 形式写入底层 writer，并发安全。
type JSONLogger struct {
	mu  sync.Mutex
	enc *json.Encoder
}

// NewJSONLogger 创建写入 w 的 JSON Lines 日志器。
func NewJSONLogger(w io.Writer) *JSONLogger {
	return &JSONLogger{enc: json.NewEncoder(w)}
}

// Log 实现 Logger。
func (l *JSONLogger) Log(entry LogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = l.enc.Encode(entry)
}

// LoggerFunc 将函数适配为 Logger。
type LoggerFunc func(entry LogEntry)

// Log 实现 Logger。
func (f LoggerFunc) Log(entry LogEntry) { f(entry) }
