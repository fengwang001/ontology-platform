package ontology

import (
	"sync"
	"time"
)

// OpKind 审计记录的操作类别。
type OpKind int

const (
	OpUpdate    OpKind = iota // 普通乐观更新尝试
	OpAcquire                 // 占用申请
	OpStage                   // 占用期间暂存变更
	OpCommit                  // 占用提交（释放）
	OpAbort                   // 占用中止（释放）
	OpHeartbeat               // 租约续期
)

func (k OpKind) String() string {
	switch k {
	case OpUpdate:
		return "update"
	case OpAcquire:
		return "acquire"
	case OpStage:
		return "stage"
	case OpCommit:
		return "commit"
	case OpAbort:
		return "abort"
	case OpHeartbeat:
		return "heartbeat"
	}
	return "unknown"
}

// AuditRecord 完整记录一次判定：输入、判定依据与结果，可用于重放核验。
// 结构化字段（ExpectedVersion/Mutations/Token）支撑确定性重放，
// Input/Basis 为人类可读的摘要。
type AuditRecord struct {
	Seq             uint64
	At              time.Time
	Kind            OpKind
	Instance        InstanceID
	Actor           string // ActionID 或 CallerID
	ExpectedVersion uint64
	Mutations       []Mutation
	Token           uint64
	Input           string // 期望版本、变更内容等输入摘要
	Basis           string // 判定依据：当前版本、占用持有方、栅栏令牌、已持有集合等
	Code            RejectCode
	NewVersion      uint64 // 成功后实例的新版本（拒绝时为判定时刻版本）
}

// AuditLog 并发安全的追加式审计日志。
type AuditLog struct {
	mu      sync.Mutex
	seq     uint64
	records []AuditRecord
}

func newAuditLog() *AuditLog { return &AuditLog{} }

func (l *AuditLog) append(rec AuditRecord) AuditRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	rec.Seq = l.seq
	l.records = append(l.records, rec)
	return rec
}

// Records 返回全部记录的快照（按 Seq 全局全序）。
func (l *AuditLog) Records() []AuditRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]AuditRecord, len(l.records))
	copy(out, l.records)
	return out
}
