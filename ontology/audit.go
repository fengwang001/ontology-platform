package ontology

import (
	"encoding/json"
	"io"
	"sync"
)

// 审计事件类别。
const (
	AuditCrash      = "crash"       // 故障注入触发的中断
	AuditRecoverDec = "recover_dec" // 恢复判定（扫描完成、修复之前）
	AuditRecover    = "recover"     // 一次恢复执行的判定与归类
	AuditBatchFinal = "batch_final" // 批次到达终态
)

// AuditEvent 是一条可重放核验的审计记录。
// 每次中断发生的阶段、每次恢复的判定依据与最终归类都会被完整记录。
type AuditEvent struct {
	Kind   string      `json:"kind"`
	Batch  BatchID     `json:"batch,omitempty"`
	Stage  string      `json:"stage,omitempty"`  // crash：中断发生的阶段点
	Status BatchStatus `json:"status,omitempty"` // batch_final：终态
	// recover：判定依据与归类结果
	CommitRecordFound bool        `json:"commit_record_found,omitempty"`
	Classification    BatchStatus `json:"classification,omitempty"`
	JournalBytes      int         `json:"journal_bytes,omitempty"`
	JournalRecords    int         `json:"journal_records,omitempty"`
	Fixes             int         `json:"fixes,omitempty"`
	Repeated          bool        `json:"repeated,omitempty"`
}

// AuditLogger 以 JSON Lines 形式顺序追加审计事件。
type AuditLogger struct {
	mu  sync.Mutex
	w   io.Writer
	buf []AuditEvent
}

// NewAuditLogger 创建写出到 w 的审计日志；w 可为 nil（仅内存留存）。
func NewAuditLogger(w io.Writer) *AuditLogger {
	return &AuditLogger{w: w}
}

// Log 追加一条审计事件。
func (l *AuditLogger) Log(ev AuditEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = append(l.buf, ev)
	if l.w != nil {
		data, _ := json.Marshal(ev)
		l.w.Write(append(data, '\n'))
	}
}

// Events 返回已记录事件的拷贝。
func (l *AuditLogger) Events() []AuditEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]AuditEvent, len(l.buf))
	copy(out, l.buf)
	return out
}

// logCrash 记录一次中断事件（由故障注入路径调用）。
func (e *Engine) logCrash(p StagePoint) {
	if e.audit != nil {
		batch := e.ctrl.ActiveBatch
		e.audit.Log(AuditEvent{Kind: AuditCrash, Batch: batch, Stage: p.String()})
	}
}
