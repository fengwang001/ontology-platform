package delegation

import (
	"sync"
	"time"
)

// OpKind 标识一次被记录调用的类型。
type OpKind string

const (
	OpGrantDirect  OpKind = "GrantDirect"
	OpRevokeDirect OpKind = "RevokeDirect"
	OpDeclare      OpKind = "Declare"
	OpRevoke       OpKind = "Revoke"
	OpCheck        OpKind = "Check"
	OpCheckAt      OpKind = "CheckAt"
)

// LogEntry 完整记录一次调用的输入、最终输出与裁决依据。
type LogEntry struct {
	Seq  uint64    // 全局单调序号，定义串行化顺序
	Time time.Time // 调用发生时的时钟时刻
	Op   OpKind

	// 输入
	Subject      string       // 目标主体（Check/CheckAt/Grant*/RevokeDirect）
	Permissions  []Permission // 涉及的权限（确定性排序）
	Delegator    string       // Declare 的委托方
	Delegatee    string       // Declare 的受托方
	AllowReleg   bool         // Declare 的是否允许再委托标记
	ValidFrom    time.Time    // Declare 的有效期起
	ValidTo      time.Time    // Declare 的有效期止
	DelegationID uint64       // Revoke 的目标委托 / Declare 成功后的新 ID
	AsOf         time.Time    // CheckAt 的历史时刻

	// 输出
	Allowed      bool     // Check/CheckAt 的判定结果
	Err          error    // 调用返回的错误（nil 表示成功）
	Witness      []uint64 // 判定为允许时，支持该判定的委托链（委托 ID 序列）
	NodesVisited int      // 本次判定遍历的委托记录数（可观测开销证明）
}

// Recorder 接收每次调用的日志条目。
type Recorder interface {
	Record(LogEntry)
}

// MemoryRecorder 是线程安全的内存日志实现，供测试与审计使用。
type MemoryRecorder struct {
	mu      sync.Mutex
	entries []LogEntry
}

// NewMemoryRecorder 创建内存日志记录器。
func NewMemoryRecorder() *MemoryRecorder { return &MemoryRecorder{} }

// Record 追加一条日志。
func (m *MemoryRecorder) Record(e LogEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, e)
}

// Entries 返回全部日志条目的副本。
func (m *MemoryRecorder) Entries() []LogEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]LogEntry, len(m.entries))
	copy(out, m.entries)
	return out
}

// discardRecorder 丢弃所有日志。
type discardRecorder struct{}

func (discardRecorder) Record(LogEntry) {}
