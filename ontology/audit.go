package ontology

import "fmt"

// OpKind 是被记录的判定操作类别。
type OpKind string

const (
	OpExpand  OpKind = "expand"
	OpMigrate OpKind = "migrate"
)

// Verdict 是判定结论。
type Verdict string

const (
	VerdictOK    Verdict = "ok"
	VerdictError Verdict = "error"
)

// DecisionRecord 记录一次判定的输入、所依据的属性定义版本与结论，
// 用于事后核查。记录在存储内追加式保存，随操作原子提交。
type DecisionRecord struct {
	Seq      int64
	Op       OpKind
	Input    string
	Verdict  Verdict
	Versions []int64 // 判定所依据的属性定义版本 ID
	Detail   string
}

// recordAudit 追加一条判定记录。审计日志有独立锁，
// 可在持有主读锁或主写锁时安全调用。
func (s *Store) recordAudit(r DecisionRecord) {
	s.auditMu.Lock()
	defer s.auditMu.Unlock()
	r.Seq = int64(len(s.audit) + 1)
	s.audit = append(s.audit, r)
}

// AuditLog 返回全部判定记录的副本。
func (s *Store) AuditLog() []DecisionRecord {
	s.auditMu.Lock()
	defer s.auditMu.Unlock()
	out := make([]DecisionRecord, len(s.audit))
	copy(out, s.audit)
	return out
}

func expandInputSummary(req ExpandRequest) string {
	pin := "none"
	if req.PinSchemaVersion != nil {
		pin = fmt.Sprintf("%d", *req.PinSchemaVersion)
	}
	return fmt.Sprintf("object=%s asOfRecord=%d valid=[%d,%d] pin=%s",
		req.ObjectID, req.AsOfRecord, req.ValidFrom, req.ValidTo, pin)
}

func migrateInputSummary(m Migration) string {
	return fmt.Sprintf("type=%s effectiveFrom=%d props=%d",
		m.TypeID, m.EffectiveFrom, len(m.NewProps))
}
