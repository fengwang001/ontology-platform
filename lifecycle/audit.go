package lifecycle

// AuditLog 是只追加的状态转换审计日志。可见性判定路径不得读取它。
type AuditLog interface {
	Append(t Transition)
}

// MemoryAuditLog 是内存实现。
type MemoryAuditLog struct {
	records []Transition
}

func NewMemoryAuditLog() *MemoryAuditLog { return &MemoryAuditLog{} }

func (l *MemoryAuditLog) Append(t Transition) { l.records = append(l.records, t) }

func (l *MemoryAuditLog) All() []Transition {
	out := make([]Transition, len(l.records))
	copy(out, l.records)
	return out
}
