package idx

import "sync"

// AuditKind 标记一次被记录的判定类别。
type AuditKind string

const (
	AuditApplied        AuditKind = "ingest-applied"   // 事件被应用（赢得 LWW）
	AuditStale          AuditKind = "ingest-stale"     // 事件因乱序到达被丢弃
	AuditDuplicate      AuditKind = "ingest-duplicate" // 重复事件，幂等忽略
	AuditBuffered       AuditKind = "ingest-buffered"  // 切换期间事件被缓冲
	AuditRejected       AuditKind = "ingest-rejected"  // 事件被拒绝（附错误）
	AuditSwitchBegin    AuditKind = "switch-begin"
	AuditSwitchCommit   AuditKind = "switch-commit"
	AuditSwitchRollback AuditKind = "switch-rollback"
	AuditClassify       AuditKind = "switch-classify" // 缓冲事件归入某索引版本
	AuditQuery          AuditKind = "query"
	AuditModelCheck     AuditKind = "model-check" // 与朴素模型对照的结论
)

// AuditEntry 记录一次判定的输入、所依据的索引版本与结论，供事后核查。
type AuditEntry struct {
	Seq     int64
	Kind    AuditKind
	Event   *Event // 相关事件（查询类条目为 nil）
	EpochID int    // 判定所依据的索引版本；-1 表示无（如切换进行中）
	Basis   string // 该索引版本的依据字段
	Detail  string // 结论描述（含错误信息、对照结果等）
}

// AuditSink 接收审计条目。
type AuditSink interface {
	Record(AuditEntry)
}

// MemAudit 是线程安全的内存审计实现，供测试与本地核查使用。
type MemAudit struct {
	mu      sync.Mutex
	entries []AuditEntry
}

func (m *MemAudit) Record(e AuditEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e.Seq = int64(len(m.entries))
	m.entries = append(m.entries, e)
}

// Entries 返回全部审计条目的副本。
func (m *MemAudit) Entries() []AuditEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]AuditEntry, len(m.entries))
	copy(out, m.entries)
	return out
}

// nopAudit 在未配置审计时丢弃条目。
type nopAudit struct{}

func (nopAudit) Record(AuditEntry) {}
