package bitemporal

import "sync"

// DecisionRecord 是一次审计判定的完整留痕：输入、所依据的规则版本与结论。
type DecisionRecord struct {
	Request     AuditRequest
	SnapshotGen int64
	// RuleBasis 为本次判定依据的规则版本起点记录时间（无规则时为 0）。
	RuleBasis int64
	Err       string
	Segments  []Segment
}

// DecisionLog 是只追加的判定日志。
type DecisionLog struct {
	mu      sync.Mutex
	records []DecisionRecord
}

// NewDecisionLog 创建空判定日志。
func NewDecisionLog() *DecisionLog { return &DecisionLog{} }

// Records 返回全部判定留痕的副本。
func (l *DecisionLog) Records() []DecisionRecord {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]DecisionRecord, len(l.records))
	copy(out, l.records)
	return out
}

func (l *DecisionLog) append(rec DecisionRecord) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = append(l.records, rec)
}
