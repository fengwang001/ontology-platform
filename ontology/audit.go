package ontologyindex

import (
	"encoding/json"
	"os"
	"sync"
)

// AuditRecord 记录一次判定的输入、所依据的索引版本与对照结论。
type AuditRecord struct {
	Op           string
	EventID      string
	ArrivedAt    int64
	EffectiveAt  LogicalClock
	IndexID      string
	IndexVersion int64
	Decision     string
	Detail       map[string]any
	ErrorCode    ErrorCode
}

// Auditor 接收审计记录（内存实现 / JSONL 文件实现）。
type Auditor interface {
	Record(r AuditRecord)
}

// MemoryAuditor 将记录保存在内存切片，供事后核查与测试断言。
type MemoryAuditor struct {
	mu      sync.Mutex
	Records []AuditRecord
}

func (a *MemoryAuditor) Record(r AuditRecord) {
	a.mu.Lock()
	a.Records = append(a.Records, r)
	a.mu.Unlock()
}

// Snapshot 返回审计记录的副本。
func (a *MemoryAuditor) Snapshot() []AuditRecord {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]AuditRecord(nil), a.Records...)
}

// JSONLAuditor 将每条记录以 JSON Lines 追加写文件，供事后核查。
type JSONLAuditor struct {
	mu  sync.Mutex
	f   *os.File
	enc *json.Encoder
}

// NewJSONLAuditor 打开（或创建）一个追加写的 JSONL 审计文件。
func NewJSONLAuditor(path string) (*JSONLAuditor, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &JSONLAuditor{f: f, enc: json.NewEncoder(f)}, nil
}

func (a *JSONLAuditor) Record(r AuditRecord) {
	a.mu.Lock()
	defer a.mu.Unlock()
	_ = a.enc.Encode(r)
}

// Close 关闭底层文件。
func (a *JSONLAuditor) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.f.Close()
}
