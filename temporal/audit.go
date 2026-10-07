package temporal

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

// AuditEntry 完整记录一次调用的输入、最终输出与据以裁决的时序依据。
type AuditEntry struct {
	Seq       int64       `json:"seq"`
	WallClock string      `json:"wall_clock"`
	Op        string      `json:"op"`
	Input     interface{} `json:"input"`
	Output    interface{} `json:"output,omitempty"`
	Error     interface{} `json:"error,omitempty"`
	Basis     []string    `json:"basis,omitempty"`
}

// AuditLog 是并发安全的 JSON Lines 审计日志。
type AuditLog struct {
	mu  sync.Mutex
	w   io.Writer
	seq int64
}

func NewAuditLog(w io.Writer) *AuditLog { return &AuditLog{w: w} }

// Append 将一次调用以单行 JSON 落盘。日志在互斥锁内生成并写入，
// 因此并发调用的日志顺序等价于某条全局串行化顺序。
func (l *AuditLog) Append(entry AuditEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	entry.Seq = l.seq
	if entry.WallClock == "" {
		entry.WallClock = time.Now().UTC().Format(time.RFC3339Nano)
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	_, err = l.w.Write(line)
	return err
}

var _ = time.Now
var _ = json.Marshal
