package ontology

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// AuditRecord 记录一次判定过程：输入、所依据的规则版本与结论，
// 用于事后核查。
type AuditRecord struct {
	When           time.Time `json:"when"`
	Kind           string    `json:"kind"` // "rebuild" 或 "compare"
	InstanceID     string    `json:"instance_id"`
	Cutoff         int64     `json:"cutoff"`
	SchemaVersion  int       `json:"schema_version"`
	EventCount     int       `json:"event_count"`
	EventsScanned  int       `json:"events_scanned"`
	CheckpointUsed bool      `json:"checkpoint_used"`
	ResultType     string    `json:"result_type,omitempty"`
	Err            string    `json:"err,omitempty"`
	// Conclusion 在 compare 记录中为 "match" 或 "mismatch"。
	Conclusion string `json:"conclusion,omitempty"`
}

// AuditLogger 接收审计记录。
type AuditLogger interface {
	Log(AuditRecord)
}

// FileAuditLogger 以 JSONL 形式追加写入审计记录。
type FileAuditLogger struct {
	mu  sync.Mutex
	enc *json.Encoder
	f   *os.File
}

// NewFileAuditLogger 创建（或追加到）指定路径的审计日志。
func NewFileAuditLogger(path string) (*FileAuditLogger, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &FileAuditLogger{enc: json.NewEncoder(f), f: f}, nil
}

// Log 追加一条记录。
func (l *FileAuditLogger) Log(rec AuditRecord) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if rec.When.IsZero() {
		rec.When = time.Now()
	}
	_ = l.enc.Encode(rec)
}

// Close 关闭底层文件。
func (l *FileAuditLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Close()
}
