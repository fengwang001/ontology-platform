package ontology

import "sync"

// AuditRecord 记录一次判定的变更内容、检查依据与结论，供事后核对。
type AuditRecord struct {
	ObjectType string
	Field      string
	Category   string
	Change     string
	Compatible bool
	Reasons    []string
	Bases      []Basis
}

type AuditSink interface {
	Record(rec AuditRecord)
}

type MemoryAuditLog struct {
	mu      sync.Mutex
	records []AuditRecord
}

func NewMemoryAuditLog() *MemoryAuditLog { return &MemoryAuditLog{} }

func (l *MemoryAuditLog) Record(rec AuditRecord) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = append(l.records, rec)
}

func (l *MemoryAuditLog) Records() []AuditRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]AuditRecord, len(l.records))
	copy(out, l.records)
	return out
}
