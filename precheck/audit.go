package precheck

import "sync"

// CrossCheckRecord 记录与朴素重演模型的逐条对照结论。
type CrossCheckRecord struct {
	Equivalent bool   `json:"equivalent"`
	Note       string `json:"note,omitempty"`
}

// AuditEntry 完整记录一次预检的输入、依据快照与结论，便于事后核查。
type AuditEntry struct {
	Seq        int64               `json:"seq"`
	Request    PrecheckRequest     `json:"request"`
	Result     PrecheckResult      `json:"result"`
	HookIDs    []string            `json:"hook_ids,omitempty"`
	PermSnap   *PermissionSnapshot `json:"perm_snapshot,omitempty"`
	Metrics    map[string]int64    `json:"metrics,omitempty"`
	CrossCheck *CrossCheckRecord   `json:"cross_check,omitempty"`
}

// AuditLog 是仅追加的审计日志（预检唯一允许的副作用通道）。
type AuditLog struct {
	mu      sync.Mutex
	entries []AuditEntry
}

func NewAuditLog() *AuditLog { return &AuditLog{} }

func (l *AuditLog) Append(e AuditEntry) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	e.Seq = int64(len(l.entries)) + 1
	l.entries = append(l.entries, e)
	return e.Seq
}

func (l *AuditLog) Entries() []AuditEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]AuditEntry, len(l.entries))
	copy(out, l.entries)
	return out
}

// Annotate 把事后对照结论批注到已写入的审计条目（仅追加式审计自身可被补全）。
func (l *AuditLog) Annotate(seq int64, cc CrossCheckRecord) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	idx := seq - 1
	if idx < 0 || int(idx) >= len(l.entries) {
		return false
	}
	l.entries[idx].CrossCheck = &cc
	return true
}

// Get 返回指定序号的审计条目副本。
func (l *AuditLog) Get(seq int64) (AuditEntry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	idx := seq - 1
	if idx < 0 || int(idx) >= len(l.entries) {
		return AuditEntry{}, false
	}
	return l.entries[idx], true
}
