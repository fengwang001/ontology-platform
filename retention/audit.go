package retention

import (
	"encoding/json"
	"io"
	"sync"
)

// AuditEntry 记录一次操作的输入、输出以及据以判定的状态与时刻。
// 日志写入发生在操作的线性化点之后，因此 Entry 中记录的状态
// 一定是该操作按全局顺序执行后的快照，不存在“两态之间”的中间形态。
type AuditEntry struct {
	Seq         int64          `json:"seq"`          // 全局单调序号（即线性化顺序）
	At          int64          `json:"at"`           // 判定时刻（时钟值）
	Op          string         `json:"op"`           // 操作名
	ObjectID    string         `json:"object_id"`    // 主体对象
	Input       map[string]any `json:"input"`        // 操作输入
	StateBefore State          `json:"state_before"` // 判定前状态（已含惰性到期提升）
	StateAfter  State          `json:"state_after"`  // 判定后状态
	Success     bool           `json:"success"`
	ErrorCode   string         `json:"error_code,omitempty"`
	Output      map[string]any `json:"output,omitempty"`
}

// AuditLog 是线程安全的审计日志；nil 安全（不记录）。
type AuditLog struct {
	mu      sync.Mutex
	w       io.Writer
	entries []AuditEntry
	enc     *json.Encoder
}

// NewAuditLog 创建审计日志。w 为每条 JSON 记录的落盘目标（可为 nil）；
// 无论 w 是否为 nil，条目都保留在内存中供测试逐条核对。
func NewAuditLog(w io.Writer) *AuditLog {
	l := &AuditLog{w: w}
	if w != nil {
		l.enc = json.NewEncoder(w)
	}
	return l
}

func (l *AuditLog) append(e AuditEntry) AuditEntry {
	if l == nil {
		return e
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	e.Seq = int64(len(l.entries)) + 1
	l.entries = append(l.entries, e)
	if l.enc != nil {
		_ = l.enc.Encode(e)
	}
	return e
}

// Entries 返回日志的副本。
func (l *AuditLog) Entries() []AuditEntry {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]AuditEntry, len(l.entries))
	copy(out, l.entries)
	return out
}

// Len 返回日志条数。
func (l *AuditLog) Len() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}
