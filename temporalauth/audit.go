package temporalauth

import "sync"

// AuditEntry 记录单次查看请求的输入、所依据的归一化基准、判定结论以及
// 与朴素逐条归一化模型的对照结论。审计内容只写入服务端，绝不进入对外响应。
type AuditEntry struct {
	RequestID string
	Seq       int64

	ObjectID   string
	ObjectType string
	Property   string
	Now        Instant
	ViewerID   string

	BasisRegionID string
	BasisVersion  int
	BasisZoneID   string
	NormalizedAt  Instant

	WindowStart Instant
	WindowEnd   Instant

	Outcome    string // "allow" | "deny" | "error"
	ErrorCode  ErrorCode
	CrossCheck bool // 是否执行了与朴素模型的对照
	CrossMatch bool // 生产结论与朴素结论是否一致
	ProbeCount int  // 本次生效版本解析探测次数（用于复杂度独立验证）
}

// AuditLog 是仅追加的线程安全审计日志。
type AuditLog struct {
	mu      sync.Mutex
	entries []AuditEntry
}

// NewAuditLog 构造审计日志。
func NewAuditLog() *AuditLog { return &AuditLog{} }

// Append 追加一条审计记录。
func (l *AuditLog) Append(e AuditEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, e)
}

// Entries 返回审计记录的快照拷贝。
func (l *AuditLog) Entries() []AuditEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]AuditEntry, len(l.entries))
	copy(out, l.entries)
	return out
}
